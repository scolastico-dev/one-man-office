// Package discover finds git repositories below a workspace root. The walk is
// bounded (depth, folders visited, total time, time per folder), never
// follows links, never descends into a repository and never enters the
// folders it is told to skip.
package discover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Root states of a Result.
const (
	StateOK        = "ok"
	StateMissing   = "missing"
	StateNotFolder = "not_folder"
	StateFailed    = "failed"
	StateCut       = "cut"
)

// Defaults and limits.
const (
	DefaultMaxDepth    = 4
	MaxMaxDepth        = 16
	DefaultMaxVisited  = 20000
	DefaultDeadline    = 2 * time.Minute
	DefaultReadTimeout = 5 * time.Second
	// MaxSkipped is the most folders a Result names in Skipped.
	MaxSkipped = 20
)

// Root is one workspace root to search.
type Root struct {
	Path     string // absolute, as written (after ~ expansion)
	MaxDepth int    // folder levels below the root; 0 means DefaultMaxDepth, capped at MaxMaxDepth
	Ignore   []string
}

// Options bounds a walk. Zero values take the defaults.
type Options struct {
	Skip        []string      // absolute folders never entered, with everything below them
	MaxVisited  int           // folders read at most; DefaultMaxVisited
	Deadline    time.Duration // the whole walk; DefaultDeadline
	ReadTimeout time.Duration // one folder; DefaultReadTimeout
	// ReadDir reads one folder; nil means os.ReadDir. Tests replace it.
	ReadDir func(string) ([]os.DirEntry, error)
}

// Repo is a repository found below a root. Path is absolute with the root's
// links resolved; Rel is slash-separated below the root ("" for the root
// itself).
type Repo struct{ Path, Rel string }

// Result reports the walk of one root. Folder names in Repos and Skipped are
// taken from the disk as they are: callers must treat them as untrusted text.
type Result struct {
	Repos   []Repo
	State   string // ok, missing, not_folder, failed, cut
	Message string
	Visited int
	Skipped []string // folders that could not be read in time (at most MaxSkipped)
	Links   int      // links to folders that were not followed
}

// errTimeout reports a folder that did not answer within the read timeout.
var errTimeout = errors.New("did not answer in time")

// ValidPattern checks an ignore pattern: path.Match syntax, not empty, no
// "**", no leading or trailing "/". Its messages never quote the pattern.
func ValidPattern(p string) error {
	if strings.TrimSpace(p) == "" {
		return errors.New("an ignore pattern must not be empty")
	}
	if strings.Contains(p, "**") {
		return errors.New("an ignore pattern cannot hold **; a pattern matches one folder name, or a path below the root when it holds /")
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return errors.New("an ignore pattern must not start or end with /")
	}
	if _, err := path.Match(p, ""); err != nil {
		return errors.New("an ignore pattern must be a valid pattern (* ? and [...] as in a shell)")
	}
	return nil
}

// Ignored reports whether a folder matches one of the patterns. A pattern
// holding "/" is matched against rel, the folder's slash-separated path below
// the root; any other pattern against base, the folder's own name. Invalid
// patterns match nothing.
func Ignored(rel, base string, patterns []string) bool {
	for _, p := range patterns {
		subject := base
		if strings.Contains(p, "/") {
			subject = rel
		}
		if ok, err := path.Match(p, subject); err == nil && ok {
			return true
		}
	}
	return false
}

// Reachable reports whether a walk of r would look at the folder at rel
// (slash-separated, below the root): it lies within the root's depth and
// neither it nor a folder above it below the root is ignored. It does not
// look at the disk. rel "" is the root itself.
func Reachable(r Root, rel string) bool {
	if rel == "" {
		return true
	}
	if path.IsAbs(rel) || path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	parts := strings.Split(rel, "/")
	if len(parts) > maxDepth(r.MaxDepth) {
		return false
	}
	for i, base := range parts {
		if Ignored(strings.Join(parts[:i+1], "/"), base, r.Ignore) {
			return false
		}
	}
	return true
}

func maxDepth(d int) int {
	if d <= 0 {
		return DefaultMaxDepth
	}
	return min(d, MaxMaxDepth)
}

// folder is a queued folder: its absolute path, its slash path below the
// root and its depth (the root is 0, its children 1).
type folder struct {
	abs, rel string
	depth    int
}

type walker struct {
	o       Options
	ctx     context.Context
	readDir func(string) ([]os.DirEntry, error)
	skip    []string
	// skipInfo identifies the skip folders that exist, so a folder that is
	// one of them under another name is never entered either.
	skipInfo []os.FileInfo
	res      Result
}

// Walk searches r breadth first. The root itself is checked first: when it
// is a repository it is the only one found. A folder is a repository when it
// holds a .git folder; a .git file or link (a linked worktree, a submodule)
// makes it no project, and the walk does not descend into either. Links are
// never followed; links to folders are counted. Right before a folder is
// read it is checked again: a folder that became a link, or whose path now
// passes through one, is not read. Folders in o.Skip and below them are
// never entered, also not under another name. Each folder is read with o.ReadTimeout; one that
// does not answer is named in Skipped and its read is left to finish on its
// own. The walk stops with state cut when ctx ends, at o.Deadline or after
// o.MaxVisited folders. Repos are sorted by Rel.
func Walk(ctx context.Context, r Root, o Options) Result {
	if o.MaxVisited <= 0 {
		o.MaxVisited = DefaultMaxVisited
	}
	if o.Deadline <= 0 {
		o.Deadline = DefaultDeadline
	}
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = DefaultReadTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, o.Deadline)
	defer cancel()
	w := &walker{o: o, ctx: ctx, readDir: o.ReadDir, res: Result{Repos: []Repo{}, Skipped: []string{}, State: StateOK}}
	if w.readDir == nil {
		w.readDir = os.ReadDir
	}
	w.walk(r)
	sort.Slice(w.res.Repos, func(i, j int) bool { return w.res.Repos[i].Rel < w.res.Repos[j].Rel })
	return w.res
}

func (w *walker) walk(r Root) {
	if !filepath.IsAbs(r.Path) {
		w.fail(StateFailed, fmt.Sprintf("%s is not an absolute path", r.Path))
		return
	}
	root, err := w.resolveRoot(filepath.Clean(r.Path))
	if err != nil {
		switch {
		case errors.Is(err, errTimeout):
			w.fail(StateFailed, fmt.Sprintf("%s did not answer within %s", r.Path, w.o.ReadTimeout))
		case errors.Is(err, os.ErrNotExist):
			w.fail(StateMissing, fmt.Sprintf("%s does not exist", r.Path))
		case errors.Is(err, errNotFolder):
			w.fail(StateNotFolder, fmt.Sprintf("%s is not a folder", r.Path))
		case errors.Is(err, errSkippedRoot):
			w.fail(StateFailed, fmt.Sprintf("%s lies inside a folder that is never searched", r.Path))
		case w.ctx.Err() != nil:
			w.stopped()
		default:
			w.fail(StateFailed, fmt.Sprintf("%s cannot be read: %v", r.Path, err))
		}
		return
	}
	if w.skipped(root) {
		w.fail(StateFailed, fmt.Sprintf("%s lies inside a folder that is never searched", r.Path))
		return
	}
	depth := maxDepth(r.MaxDepth)
	queue := []folder{{abs: root}}
	for len(queue) > 0 {
		if w.ctx.Err() != nil {
			w.stopped()
			return
		}
		if w.res.Visited >= w.o.MaxVisited {
			w.cut(fmt.Sprintf("stopped after %d folders, the most one search reads", w.o.MaxVisited))
			return
		}
		f := queue[0]
		queue = queue[1:]
		w.res.Visited++
		entries, err := w.read(f.abs)
		if err != nil {
			if w.ctx.Err() != nil {
				w.stopped()
				return
			}
			if f.depth == 0 {
				if errors.Is(err, errTimeout) {
					w.fail(StateFailed, fmt.Sprintf("%s did not answer within %s", r.Path, w.o.ReadTimeout))
				} else {
					w.fail(StateFailed, fmt.Sprintf("%s cannot be read: %v", r.Path, err))
				}
				return
			}
			if errors.Is(err, errTimeout) && len(w.res.Skipped) < MaxSkipped {
				w.res.Skipped = append(w.res.Skipped, f.abs)
			}
			// A folder that cannot be read is passed over.
			continue
		}
		if git, ok := findGit(entries); ok {
			if git.isDir {
				w.res.Repos = append(w.res.Repos, Repo{Path: f.abs, Rel: f.rel})
			}
			// A repository, a linked worktree or a submodule: never
			// descended into.
			continue
		}
		for _, e := range entries {
			if e.dirLink {
				w.res.Links++
				continue
			}
			if !e.isDir || f.depth+1 > depth {
				continue
			}
			rel := e.name
			if f.rel != "" {
				rel = f.rel + "/" + e.name
			}
			if Ignored(rel, e.name, r.Ignore) {
				continue
			}
			abs := filepath.Join(f.abs, e.name)
			if w.skipped(abs) {
				continue
			}
			if w.res.Visited+len(queue) >= w.o.MaxVisited {
				// The queue never holds more than can still be read.
				w.cut(fmt.Sprintf("stopped after %d folders, the most one search reads", w.o.MaxVisited))
				return
			}
			queue = append(queue, folder{abs: abs, rel: rel, depth: f.depth + 1})
		}
	}
}

var errNotFolder = errors.New("not a folder")

// errSkippedRoot reports a root that is a skip folder under another name.
var errSkippedRoot = errors.New("a folder that is never searched")

// errChanged reports a queued folder that is no longer the folder the walk
// saw, or is a folder the walk never enters.
var errChanged = errors.New("changed while it was searched")

// resolveRoot resolves the root's links and the skip folders' links within
// the read timeout and checks that the root is a folder.
func (w *walker) resolveRoot(root string) (string, error) {
	type resolved struct {
		root string
		skip []string
		info []os.FileInfo
	}
	skips := w.o.Skip
	got, err := timed(w, func(<-chan struct{}) (resolved, error) {
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			return resolved{}, err
		}
		info, err := os.Stat(real)
		if err != nil {
			return resolved{}, err
		}
		if !info.IsDir() {
			return resolved{}, errNotFolder
		}
		var out []string
		var infos []os.FileInfo
		for _, s := range skips {
			if s == "" || !filepath.IsAbs(s) {
				continue
			}
			s = filepath.Clean(s)
			out = append(out, s)
			if r, err := filepath.EvalSymlinks(s); err == nil && r != s {
				out = append(out, r)
			}
			if si, err := os.Stat(s); err == nil {
				if os.SameFile(info, si) {
					return resolved{}, errSkippedRoot
				}
				infos = append(infos, si)
			}
		}
		return resolved{real, out, infos}, nil
	})
	if err != nil {
		return "", err
	}
	w.skip, w.skipInfo = got.skip, got.info
	return got.root, nil
}

// skipped reports whether p is a skip folder or lies below one.
func (w *walker) skipped(p string) bool {
	for _, s := range w.skip {
		if p == s {
			return true
		}
		prefix := s
		if !strings.HasSuffix(prefix, string(filepath.Separator)) {
			prefix += string(filepath.Separator)
		}
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// entry is what the walk needs of one directory entry.
type entry struct {
	name    string
	isDir   bool // a folder, not a link
	dirLink bool // a link that points to a folder
}

func findGit(entries []entry) (entry, bool) {
	for _, e := range entries {
		if e.name == ".git" {
			return e, true
		}
	}
	return entry{}, false
}

// recheck checks a queued folder right before it is read: it must still be
// a folder, not a link; its path, resolved, must be the path the walk built
// (no folder on the way became a link); and it must not be a skip folder,
// by resolved path or as the same folder under another name.
func (w *walker) recheck(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errChanged
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if !samePath(real, dir) || w.skipped(real) {
		return errChanged
	}
	for _, s := range w.skipInfo {
		if os.SameFile(info, s) {
			return errChanged
		}
	}
	return nil
}

// samePath compares two resolved paths; Windows ignores case.
func samePath(a, b string) bool {
	return a == b || (runtime.GOOS == "windows" && strings.EqualFold(a, b))
}

// read lists one folder within the read timeout, after recheck. Links are
// checked for pointing to a folder inside the same time limit.
func (w *walker) read(dir string) ([]entry, error) {
	return timed(w, func(quit <-chan struct{}) ([]entry, error) {
		if err := w.recheck(dir); err != nil {
			return nil, err
		}
		des, err := w.readDir(dir)
		if err != nil {
			return nil, err
		}
		list := make([]entry, 0, len(des))
		for _, d := range des {
			select {
			case <-quit:
				return nil, errTimeout
			default:
			}
			e := entry{name: d.Name()}
			switch t := d.Type(); {
			case t&os.ModeSymlink != 0:
				if info, err := os.Stat(filepath.Join(dir, e.name)); err == nil && info.IsDir() {
					e.dirLink = true
				}
			case t.IsDir():
				e.isDir = true
			}
			list = append(list, e)
		}
		return list, nil
	})
}

// timed runs fn in its own goroutine and waits for it at most the read
// timeout, or until the walk's context ends. fn's result is taken only when
// it finished in time; quit is closed when the caller stopped waiting, and a
// late result is dropped.
func timed[T any](w *walker, fn func(quit <-chan struct{}) (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	quit := make(chan struct{})
	done := make(chan result, 1)
	go func() {
		v, err := fn(quit)
		done <- result{v, err}
	}()
	t := time.NewTimer(w.o.ReadTimeout)
	defer t.Stop()
	var zero T
	select {
	case r := <-done:
		return r.v, r.err
	case <-t.C:
		close(quit)
		return zero, errTimeout
	case <-w.ctx.Done():
		close(quit)
		return zero, w.ctx.Err()
	}
}

func (w *walker) fail(state, msg string) {
	w.res.State, w.res.Message = state, msg
}

func (w *walker) cut(msg string) {
	w.res.State, w.res.Message = StateCut, msg
}

func (w *walker) stopped() {
	if errors.Is(w.ctx.Err(), context.DeadlineExceeded) {
		w.cut(fmt.Sprintf("stopped at the time limit of %s", w.o.Deadline))
		return
	}
	w.cut("the search was stopped")
}
