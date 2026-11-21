package lsp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Options supplies the managed-worktree boundary and current configuration.
type Options struct {
	Worktrees string
	Settings  func() api.LSPSettings
	// Report runs on a runtime's debounce worker. It must return promptly and
	// must not call Attach, Detach or Close; queue delivery outside this callback.
	Report func(Report)
	Log    *log.Logger
	// PollInterval defaults to 250ms. Polling observes atomic replacements and
	// newly created directories without a platform-specific recursive watcher.
	PollInterval time.Duration
}

// Manager owns server processes, polling and diagnostics until the last agent
// leaves a worktree. Construct once per engine; Close waits for all children.
type Manager struct {
	o              Options
	mu             sync.Mutex
	roots          map[string]*worktree
	sessions       map[string][]string
	closed         bool
	subscribers    map[uint64]func(Report)
	nextSubscriber uint64
}

func New(o Options) *Manager {
	if o.PollInterval <= 0 {
		o.PollInterval = 250 * time.Millisecond
	}
	if o.Log == nil {
		o.Log = log.New(io.Discard, "", 0)
	}
	if o.Report == nil {
		o.Report = func(Report) {}
	}
	return &Manager{o: o, roots: map[string]*worktree{}, sessions: map[string][]string{}, subscribers: map[uint64]func(Report){}}
}
func (m *Manager) settings() api.LSPSettings {
	if m.o.Settings == nil {
		return api.LSPSettings{}
	}
	return m.o.Settings()
}
func within(base, dir string) bool {
	rel, err := filepath.Rel(base, dir)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && len(rel) > 0 && !(len(rel) > 3 && rel[:3] == ".."+string(filepath.Separator))
}
func (m *Manager) validRoot(root string) (string, error) {
	base, err := filepath.EvalSymlinks(m.o.Worktrees)
	if err != nil {
		return "", err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return "", err
	}
	// Resolve parent aliases (including macOS /var), but reject a worktree
	// directory replaced by a symlink to another checkout.
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	if real != filepath.Join(parent, filepath.Base(abs)) || !within(base, real) {
		return "", errors.New("LSP root is not a managed worktree")
	}
	info, err := os.Lstat(filepath.Join(real, ".git"))
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("LSP root has no managed gitfile")
	}
	return real, nil
}

// Attach binds a running session to the engine's recorded worktrees. The
// caller must first validate them against the project's Git worktree records.
// Projectless folders and source checkouts are rejected. Repeated attachment
// of the same session is idempotent. Startup failures appear in Scan and logs.
func (m *Manager) Attach(session string, roots []string) error {
	if session == "" {
		return errors.New("LSP session is empty")
	}
	validated := []string{}
	seen := map[string]bool{}
	for _, root := range roots {
		r, err := m.validRoot(root)
		if err != nil {
			return err
		}
		if !seen[r] {
			validated = append(validated, r)
			seen[r] = true
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("LSP manager closed")
	}
	if _, ok := m.sessions[session]; ok {
		return nil
	}
	m.sessions[session] = validated
	for _, root := range validated {
		r := m.roots[root]
		if r == nil {
			ctx, cancel := context.WithCancel(context.Background())
			r = &worktree{manager: m, root: root, ctx: ctx, cancel: cancel, done: make(chan struct{}), scans: make(chan chan error), servers: map[string]*languageServer{}}
			r.issues = newIssues(root, time.Duration(m.settings().Debounce), m.publish)
			m.roots[root] = r
			r.issues.attach(session)
			go r.run()
		} else {
			r.issues.attach(session)
		}
	}
	return nil
}

// Detach removes notification eligibility immediately, then stops and reaps
// runtimes no longer used by any session. Ignore state ends with the runtime.
func (m *Manager) Detach(session string) {
	m.mu.Lock()
	roots := m.sessions[session]
	delete(m.sessions, session)
	stopped := []*worktree{}
	for _, root := range roots {
		r := m.roots[root]
		if r == nil {
			continue
		}
		r.issues.detach(session)
		used := false
		for _, rs := range m.sessions {
			for _, v := range rs {
				if v == root {
					used = true
				}
			}
		}
		if !used {
			delete(m.roots, root)
			r.cancel()
			stopped = append(stopped, r)
		}
	}
	m.mu.Unlock()
	for _, r := range stopped {
		<-r.done
	}
}

// Snapshot returns the current diagnostics (including suppressed ones) in
// the session's worktrees. Suppression filters reports, not inspection.
func (m *Manager) Snapshot(session string) []Issue {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Issue{}
	for _, root := range m.sessions[session] {
		if r := m.roots[root]; r != nil {
			out = append(out, r.issues.snapshot()...)
		}
	}
	return out
}

// Ignore suppresses a current issue only within an attached session's runtime.
// It removes queued reports too; it sends no invented LSP protocol request.
func (m *Manager) Ignore(session, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, root := range m.sessions[session] {
		if r := m.roots[root]; r != nil && r.issues.ignore(id) {
			return true
		}
	}
	return false
}

// Scan synchronizes current disk content immediately and waits for advertised
// pull diagnostics. Push diagnostics remain asynchronous. Polling does this
// automatically; callers may request it after a known batch of writes.
func (m *Manager) Scan(ctx context.Context, root string) error {
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	root, _ = filepath.Abs(root)
	m.mu.Lock()
	r := m.roots[filepath.Clean(root)]
	m.mu.Unlock()
	if r == nil {
		return fmt.Errorf("no LSP runtime for %s", root)
	}
	reply := make(chan error, 1)
	select {
	case r.scans <- reply:
	case <-r.done:
		return errors.New("LSP runtime stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-reply:
		return err
	case <-r.done:
		return errors.New("LSP runtime stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	roots := []*worktree{}
	for _, r := range m.roots {
		r.cancel()
		roots = append(roots, r)
	}
	m.mu.Unlock()
	for _, r := range roots {
		<-r.done
	}
	m.mu.Lock()
	m.roots = map[string]*worktree{}
	m.sessions = map[string][]string{}
	m.mu.Unlock()
}

// Worktrees returns the managed roots attached to a session.
func (m *Manager) Worktrees(session string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.sessions[session]...)
}

// Subscribe registers a transport-independent report consumer. Callbacks run
// serially per worktree and must queue delivery promptly; they must not call
// Attach, Detach or Close. The returned function stops future callbacks (an
// already executing callback may finish). Report.Sessions controls eligibility.
func (m *Manager) Subscribe(report func(Report)) func() {
	m.mu.Lock()
	m.nextSubscriber++
	id := m.nextSubscriber
	m.subscribers[id] = report
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.subscribers, id); m.mu.Unlock() }
}
func (m *Manager) publish(r Report) {
	m.mu.Lock()
	callbacks := make([]func(Report), 0, len(m.subscribers))
	for _, f := range m.subscribers {
		callbacks = append(callbacks, f)
	}
	m.mu.Unlock()
	m.o.Report(r)
	for _, f := range callbacks {
		f(r)
	}
}
