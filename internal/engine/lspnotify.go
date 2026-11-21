package engine

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/lsp"
)

// LSPListeners are the MCP notification streams agent sessions have open.
type LSPListeners interface {
	// Notify sends text over the session's confirmed stream and reports
	// whether one took it. It is false for a session with no established
	// stream, and for one whose stream failed just now; the engine then tells
	// the agent in chat instead.
	Notify(session, text string) bool
}

// lspDelivery hands language-server reports from the runtime's debounce
// worker, which must return promptly, to a goroutine that delivers them in
// the order they arrived.
type lspDelivery struct {
	mu      sync.Mutex
	pending []lsp.Report
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	unsub   func()
}

// subscribeLSP delivers the reports of the engine's LSP runtime to the
// sessions they name. Calling it again moves the delivery to the current
// runtime (a test replaces the manager).
func (e *Engine) subscribeLSP() {
	e.stopLSPDelivery()
	d := &e.lspOut
	d.mu.Lock()
	d.pending = nil
	d.wake = make(chan struct{}, 1)
	d.stop = make(chan struct{})
	d.done = make(chan struct{})
	wake, stop, done := d.wake, d.stop, d.done
	d.mu.Unlock()
	unsub := e.lsp.Subscribe(func(r lsp.Report) {
		d.mu.Lock()
		d.pending = append(d.pending, r)
		d.mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	d.mu.Lock()
	d.unsub = unsub
	d.mu.Unlock()
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-wake:
			}
			for {
				d.mu.Lock()
				if len(d.pending) == 0 {
					d.mu.Unlock()
					break
				}
				r := d.pending[0]
				d.pending = d.pending[1:]
				d.mu.Unlock()
				e.deliverLSP(r)
			}
		}
	}()
}

// stopLSPDelivery ends the delivery goroutine; reports not yet delivered are
// dropped.
func (e *Engine) stopLSPDelivery() {
	d := &e.lspOut
	d.mu.Lock()
	unsub, stop, done := d.unsub, d.stop, d.done
	d.unsub, d.stop, d.done = nil, nil, nil
	d.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if stop != nil {
		close(stop)
		<-done
	}
}

// sessionCLI is the program of a live session's profile; "" for an unknown
// session.
func (e *Engine) sessionCLI(id string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if en := e.entries[id]; en != nil {
		return en.cli
	}
	return ""
}

// deliverLSP tells each session of the report about its new issues: over its
// MCP notification stream when it has a confirmed one, and in chat otherwise.
// The explicit force-chat setting, by program, wins over a stream. A session
// is never told twice.
func (e *Engine) deliverLSP(r lsp.Report) {
	if len(r.Issues) == 0 {
		return
	}
	text := lspText(r)
	force := e.st.General().LSP.ForceChat
	for _, session := range r.Sessions {
		if e.o.Listeners != nil && !slices.Contains(force, e.sessionCLI(session)) && e.o.Listeners.Notify(session, text) {
			continue
		}
		e.Notify(session, text)
	}
}

// maxLSPText leaves room for the notification prefix and the closing advice
// inside agents.MaxNotifyLength.
const maxLSPText = agents.MaxNotifyLength - 250

// lspText words a report for an agent: each issue with its file, position,
// severity, message and the id `omo ai lsp ignore` takes. Messages come from
// a language server, so they are only ever data inside this sentence.
func lspText(r lsp.Report) string {
	var b strings.Builder
	n := len(r.Issues)
	fmt.Fprintf(&b, "Language server: %d new issue", n)
	if n != 1 {
		b.WriteByte('s')
	}
	fmt.Fprintf(&b, " in %s:", r.Worktree)
	shown := 0
	for _, i := range r.Issues {
		item := fmt.Sprintf(" %s:%d:%d %s: %s (id %s);", i.File, i.Range.Start.Line+1, i.Range.Start.Character+1,
			severityName(i.Severity), strings.TrimSpace(i.Message), i.ID)
		if shown > 0 && b.Len()+len(item) > maxLSPText {
			break
		}
		b.WriteString(item)
		shown++
	}
	if shown < n {
		fmt.Fprintf(&b, " and %d more (they are reported again when they change).", n-shown)
	}
	b.WriteString(" Fix them, or silence a wrong report with `omo ai lsp ignore <id>` (MCP tool lsp_ignore).")
	return b.String()
}

func severityName(s int) string {
	switch s {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "information"
	case 4:
		return "hint"
	}
	return "issue"
}

// IgnoreLSPIssue suppresses the issue id in the worktrees of the caller's
// own session: it is no longer reported there, queued or in the future. Only
// the working roles have a monitored worktree; an id that names no issue of
// the caller's worktrees is not found, whatever other sessions see.
func (e *Engine) IgnoreLSPIssue(a board.Actor, id string) (api.LSPIgnoreResult, error) {
	switch {
	case a.Session == "" || a.Purpose != api.PurposeWork:
		return api.LSPIgnoreResult{}, api.Errorf(api.CodeForbidden, "only a working agent session has monitored worktrees to ignore issues in")
	case a.Role != api.RolePM && a.Role != api.RoleDeveloper && a.Role != api.RoleReviewer:
		return api.LSPIgnoreResult{}, api.Errorf(api.CodeForbidden, "the %s has no monitored worktree", a.Role)
	case strings.TrimSpace(id) == "":
		return api.LSPIgnoreResult{}, api.Errorf(api.CodeBadRequest, "the issue id is empty")
	}
	var found *lsp.Issue
	for _, i := range e.lsp.Snapshot(a.Session) {
		if i.ID == id {
			found = &i
			break
		}
	}
	if found == nil || !e.lsp.Ignore(a.Session, id) {
		return api.LSPIgnoreResult{}, api.Errorf(api.CodeNotFound, "no language-server issue %q in your worktree", id)
	}
	return api.LSPIgnoreResult{ID: id, Ignored: true, Worktree: found.Worktree, File: found.File,
		Line: found.Range.Start.Line + 1, Column: found.Range.Start.Character + 1, Message: found.Message}, nil
}
