package engine

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// diskState is what the engine remembers about worktrees and exported
// branches during one run. The zero value is ready to use.
type diskState struct {
	mu sync.Mutex
	// holds counts the open holds per worktree folder (see holdKey).
	holds map[string]int
	// noted remembers the comments about kept worktrees posted in this
	// run, so each is posted once.
	noted map[string]bool
	// exportsChecked is when merged exports were last looked for;
	// exportsRunning is set while a check runs.
	exportsChecked time.Time
	exportsRunning bool
}

// HoldWorktree keeps the worktree at path from being removed until release
// is called. Holds are counted: the worktree may go once every hold is
// released, and the last release wakes the engine so that a cleanup that
// skipped the folder runs again. Calling release more than once releases
// once.
func (e *Engine) HoldWorktree(path string) (release func()) {
	key := holdKey(path)
	d := &e.disk
	d.mu.Lock()
	if d.holds == nil {
		d.holds = map[string]int{}
	}
	d.holds[key]++
	d.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			d.holds[key]--
			last := d.holds[key] <= 0
			if last {
				delete(d.holds, key)
			}
			d.mu.Unlock()
			if last {
				e.retryCleanups()
				e.Wake()
			}
		})
	}
}

// held reports whether a user holds the worktree at path.
func (e *Engine) held(path string) bool {
	key := holdKey(path)
	e.disk.mu.Lock()
	defer e.disk.mu.Unlock()
	return e.disk.holds[key] > 0
}

// removeUnheld runs remove, which removes the worktree at path, unless a
// user holds it, and reports whether it ran. The check and the removal run
// under the lock HoldWorktree takes, so a hold either comes first and keeps
// the folder, or waits until the removal is over. remove must not take
// that lock itself.
func (e *Engine) removeUnheld(path string, remove func()) bool {
	key := holdKey(path)
	e.at("before-remove")
	e.disk.mu.Lock()
	defer e.disk.mu.Unlock()
	if e.disk.holds[key] > 0 {
		return false
	}
	remove()
	return true
}

// retryCleanups lets cleanups that were tried on the current board run
// again on the next pass.
func (e *Engine) retryCleanups() {
	prefix := actCleanup.String() + "|"
	e.mu.Lock()
	defer e.mu.Unlock()
	for k := range e.tried {
		if strings.HasPrefix(k, prefix) {
			delete(e.tried, k)
		}
	}
}

// holdKey names a folder the same way however it is written.
func holdKey(path string) string {
	p, err := filepath.Abs(path)
	if err != nil {
		p = filepath.Clean(path)
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return p
}

// noteOnce reports whether the comment named key was not posted in this
// run yet, and records it as posted.
func (e *Engine) noteOnce(key string) bool {
	e.disk.mu.Lock()
	defer e.disk.mu.Unlock()
	if e.disk.noted == nil {
		e.disk.noted = map[string]bool{}
	}
	if e.disk.noted[key] {
		return false
	}
	e.disk.noted[key] = true
	return true
}

// forgetNote lets the comment named key be posted again.
func (e *Engine) forgetNote(key string) {
	e.disk.mu.Lock()
	defer e.disk.mu.Unlock()
	delete(e.disk.noted, key)
}

// maxListed is how many file names a comment or message lists.
const maxListed = 20

// showPath writes a path so that it stays on one line and inside a code
// span, and shows the characters that hide or reorder text as escapes.
func showPath(p string) string {
	if strings.ContainsAny(p, "`\\\"") || strings.IndexFunc(p, func(r rune) bool {
		return textsafe.IsHidden(r)
	}) >= 0 {
		return strings.ReplaceAll(strconv.Quote(p), "`", `\x60`)
	}
	return p
}

// code writes s as an inline code span, so that nothing in it is read as a
// mention.
func code(s string) string { return "`" + showPath(s) + "`" }

// fileList lists files as Markdown list items, at most maxListed of them.
func fileList(files []string) string {
	var b strings.Builder
	for i, f := range files {
		if i == maxListed {
			fmt.Fprintf(&b, "- and %d more\n", len(files)-maxListed)
			break
		}
		fmt.Fprintf(&b, "- %s\n", code(f))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// joinCapped joins at most maxListed files for one line of text.
func joinCapped(files []string) string {
	names := make([]string, 0, min(len(files), maxListed))
	for i, f := range files {
		if i == maxListed {
			break
		}
		names = append(names, showPath(f))
	}
	out := strings.Join(names, ", ")
	if len(files) > maxListed {
		out += fmt.Sprintf(" and %d more", len(files)-maxListed)
	}
	return out
}
