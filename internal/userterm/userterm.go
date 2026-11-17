// Package userterm runs the user's own shells in a project folder, a ticket
// worktree or the user's home folder. A shell is an ordinary login-less interactive shell on a
// pseudo-terminal, started with the user's environment and no agent identity,
// so commands typed in it act as the user. Shells outlive browser connections
// and end when asked, when their process exits, or when the manager closes.
package userterm

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/webterm"
)

const (
	defaultMaxRunning = 16
	defaultMaxKept    = 32
	// closeWait bounds how long Close waits for a killed shell to be reaped.
	closeWait = 5 * time.Second
)

// Errors returned by the manager.
var (
	ErrTooMany = errors.New("too many terminals are open")
	ErrUnknown = errors.New("unknown terminal")
	ErrClosed  = errors.New("terminals are closed")
)

// Spec describes a shell to open. Dir must exist and be a directory; whether
// the caller may open a shell there is decided by the caller.
type Spec struct {
	Dir, Title, Project string
	Ticket              *int
	// Hold takes Options.Hold(Dir) while the shell runs.
	Hold bool
	// Verify, when set, is called right before the shell process starts;
	// an error refuses the start and is returned by Open as it is. It lets
	// the caller check again that Dir is still the folder it checked.
	Verify func() error
}

// Info describes a shell. State is "running" or "exited".
type Info struct {
	ID        string
	Title     string
	Dir       string
	Project   string
	Ticket    *int
	State     string
	ExitError string
	StartedAt time.Time
	EndedAt   *time.Time
}

// Change reports a change of the set of shells. Kind is "opened", "exited" or
// "closed".
type Change struct {
	Kind string
	Info Info
}

// Options configures a Manager.
type Options struct {
	// Env returns the full environment of a new shell (see ShellEnv). Nil uses
	// ShellEnv(childenv.Inherited(), "").
	Env func() []string
	// Hold takes a hold on a directory and returns its release. Nil: no holds.
	Hold func(dir string) (release func())
	// OnChange is called in order, outside the manager's lock. It must not
	// block and must not call Close or CloseAll.
	OnChange func(Change)
	// MaxRunning and MaxKept default to 16 and 32.
	MaxRunning, MaxKept int
	// Shell returns the shell command; nil uses DefaultShell.
	Shell func() (cmd string, args []string)
	Now   func() time.Time
}

type shell struct {
	info     Info
	term     *webterm.Terminal
	holdOnce sync.Once
	release  func()
	ready    chan struct{} // closed once the shell is registered and announced
	gone     bool          // forgotten: no further changes are reported
}

func (s *shell) releaseHold() {
	s.holdOnce.Do(func() {
		if s.release != nil {
			s.release()
		}
	})
}

// Manager owns the user's shells.
type Manager struct {
	o Options

	mu       sync.Mutex
	shells   map[string]*shell
	order    []string // oldest first
	starting int
	closed   bool
	pending  []Change

	emitMu sync.Mutex
}

// New returns a manager.
func New(o Options) *Manager {
	if o.MaxRunning <= 0 {
		o.MaxRunning = defaultMaxRunning
	}
	if o.MaxKept <= 0 {
		o.MaxKept = defaultMaxKept
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Shell == nil {
		o.Shell = DefaultShell
	}
	if o.Env == nil {
		o.Env = func() []string { return ShellEnv(childenv.Inherited(), "") }
	}
	return &Manager{o: o, shells: map[string]*shell{}}
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (m *Manager) runningLocked() int {
	n := m.starting
	for _, s := range m.shells {
		if s.info.State == "running" {
			n++
		}
	}
	return n
}

// queue records a change; the caller holds m.mu and calls flush after
// unlocking.
func (m *Manager) queue(kind string, info Info) {
	m.pending = append(m.pending, Change{Kind: kind, Info: info})
}

// flush delivers queued changes in order, outside m.mu.
func (m *Manager) flush() {
	m.emitMu.Lock()
	defer m.emitMu.Unlock()
	for {
		m.mu.Lock()
		batch := m.pending
		m.pending = nil
		m.mu.Unlock()
		if len(batch) == 0 {
			return
		}
		if m.o.OnChange == nil {
			continue
		}
		for _, c := range batch {
			m.o.OnChange(c)
		}
	}
}

// Open starts a shell in s.Dir. It returns ErrTooMany when MaxRunning shells
// are running and ErrClosed after CloseAll.
func (m *Manager) Open(s Spec) (Info, error) {
	if s.Dir == "" {
		return Info{}, errors.New("terminal directory is empty")
	}
	st, err := os.Stat(s.Dir)
	if err != nil {
		return Info{}, fmt.Errorf("terminal directory: %w", err)
	}
	if !st.IsDir() {
		return Info{}, fmt.Errorf("terminal directory %q is not a directory", s.Dir)
	}
	id, err := newID()
	if err != nil {
		return Info{}, err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Info{}, ErrClosed
	}
	if m.runningLocked() >= m.o.MaxRunning {
		m.mu.Unlock()
		return Info{}, ErrTooMany
	}
	m.starting++
	m.mu.Unlock()

	sh := &shell{ready: make(chan struct{})}
	if s.Hold && m.o.Hold != nil {
		sh.release = m.o.Hold(s.Dir)
	}
	fail := func(err error) (Info, error) {
		sh.releaseHold()
		m.mu.Lock()
		m.starting--
		m.mu.Unlock()
		return Info{}, err
	}

	cmd, args := m.o.Shell()
	env := stripIdentity(m.o.Env())
	if s.Verify != nil {
		if err := s.Verify(); err != nil {
			return fail(err)
		}
	}
	term, err := webterm.Start(webterm.Options{
		ID: id, Command: cmd, Args: args, Env: env, Dir: s.Dir,
		OnExit: func(t *webterm.Terminal, exitErr error) { m.exited(sh, t, exitErr) },
	})
	if err != nil {
		return fail(fmt.Errorf("start shell: %w", err))
	}

	m.mu.Lock()
	m.starting--
	sh.term = term
	sh.info = Info{ID: id, Title: s.Title, Dir: s.Dir, Project: s.Project, State: "running", StartedAt: m.o.Now()}
	if s.Ticket != nil {
		n := *s.Ticket
		sh.info.Ticket = &n
	}
	if m.closed {
		sh.gone = true
		m.mu.Unlock()
		close(sh.ready)
		_ = term.Kill()
		waitDone(term, closeWait)
		sh.releaseHold()
		return Info{}, ErrClosed
	}
	m.shells[id] = sh
	m.order = append(m.order, id)
	info := copyInfo(sh.info)
	m.queue("opened", copyInfo(info))
	m.mu.Unlock()
	close(sh.ready)
	m.flush()
	return info, nil
}

// exited runs from the terminal's exit callback.
func (m *Manager) exited(sh *shell, t *webterm.Terminal, exitErr error) {
	<-sh.ready
	sh.releaseHold()
	m.mu.Lock()
	if sh.gone {
		m.mu.Unlock()
		return
	}
	now := m.o.Now()
	sh.info.State = "exited"
	sh.info.EndedAt = &now
	if exitErr != nil {
		sh.info.ExitError = exitErr.Error()
	}
	m.queue("exited", copyInfo(sh.info))
	m.dropOldExitedLocked()
	m.mu.Unlock()
	m.flush()
}

// dropOldExitedLocked forgets the oldest exited shells beyond MaxKept.
func (m *Manager) dropOldExitedLocked() {
	exited := 0
	for _, s := range m.shells {
		if s.info.State == "exited" {
			exited++
		}
	}
	for _, id := range append([]string(nil), m.order...) {
		if exited <= m.o.MaxKept {
			return
		}
		s := m.shells[id]
		if s != nil && s.info.State == "exited" {
			m.forgetLocked(s)
			exited--
		}
	}
}

// forgetLocked removes s and queues its closed change.
func (m *Manager) forgetLocked(s *shell) {
	s.gone = true
	delete(m.shells, s.info.ID)
	for i, id := range m.order {
		if id == s.info.ID {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.queue("closed", copyInfo(s.info))
}

func copyInfo(i Info) Info {
	if i.Ticket != nil {
		n := *i.Ticket
		i.Ticket = &n
	}
	if i.EndedAt != nil {
		t := *i.EndedAt
		i.EndedAt = &t
	}
	return i
}

func waitDone(t *webterm.Terminal, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-t.Done():
	case <-timer.C:
	}
}

// List returns the known shells, oldest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, copyInfo(m.shells[id].info))
	}
	return out
}

// Terminal returns the terminal of a known shell, running or exited.
func (m *Manager) Terminal(id string) (*webterm.Terminal, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.shells[id]
	if !ok {
		return nil, false
	}
	return s.term, true
}

// Close ends a running shell, releases its hold and forgets it. It returns
// ErrUnknown when no such shell exists.
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	s, ok := m.shells[id]
	if !ok {
		m.mu.Unlock()
		return ErrUnknown
	}
	m.forgetLocked(s)
	m.mu.Unlock()
	m.flush()
	_ = s.term.Kill()
	waitDone(s.term, closeWait)
	s.releaseHold()
	return nil
}

// CloseAll ends every shell, waiting at most grace for them to be reaped, and
// releases every hold. Open refuses afterwards.
func (m *Manager) CloseAll(grace time.Duration) {
	m.mu.Lock()
	m.closed = true
	var all []*shell
	for _, id := range append([]string(nil), m.order...) {
		s := m.shells[id]
		all = append(all, s)
		m.forgetLocked(s)
	}
	m.mu.Unlock()
	m.flush()
	for _, s := range all {
		_ = s.term.Kill()
	}
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	for _, s := range all {
		select {
		case <-s.term.Done():
		case <-deadline.C:
			deadline.Reset(0)
		}
	}
	for _, s := range all {
		s.releaseHold()
	}
}

// stripIdentity removes agent identity variables from env, whatever the
// caller put there.
func stripIdentity(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if isIdentityKey(e) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func isIdentityKey(entry string) bool {
	key, _, _ := strings.Cut(entry, "=")
	key = strings.ToUpper(key)
	return key == "OMO_AGENT_ID" || strings.HasPrefix(key, "OMO_FAKE")
}

// ShellEnv returns the environment of a user shell: base cleaned of omo
// control variables, agent identity (OMO_AGENT_ID) and fake-agent settings,
// plus OMO_SOCKET (when socket is not empty), TERM and COLORTERM.
func ShellEnv(base []string, socket string) []string {
	var env []string
	for _, e := range webterm.CleanEnvironment(base) {
		key, _, _ := strings.Cut(e, "=")
		switch strings.ToUpper(key) {
		case "TERM", "COLORTERM", "COLORFGBG":
			continue
		}
		if isIdentityKey(e) {
			continue
		}
		env = append(env, e)
	}
	if socket != "" {
		env = append(env, "OMO_SOCKET="+socket)
	}
	return append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
}
