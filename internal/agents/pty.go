package agents

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/claudetrust"
	"github.com/scolastico-dev/one-man-office/internal/session"
	"github.com/scolastico-dev/one-man-office/internal/webterm"
)

// Transcript bounds of every agent.
const (
	LogMaxSizeKB = 10240
	LogKeep      = 3
)

// PTYRunner starts agents in pseudo-terminals (ConPTY on Windows).
type PTYRunner struct {
	// Now is the clock of the notification hold-back (nil: time.Now).
	Now func() time.Time
	// Debounce is how long a notification waits after the last key typed
	// into the terminal: 0 means DefaultDebounce, negative means no wait.
	Debounce time.Duration
	// EnvTimeout bounds each backtick command in the agent environment
	// (0: session.DefaultCommandTimeout). Start fails when one times out.
	EnvTimeout time.Duration
}

// NewPTYRunner returns a runner with the default hold-back.
func NewPTYRunner() *PTYRunner { return &PTYRunner{} }

type ptyAgent struct {
	id       string
	termID   string
	sess     *session.Session
	term     *webterm.Terminal
	notes    *notifier
	started  time.Time
	delivery chan struct{} // closed when start-prompt delivery is over

	mu       sync.Mutex
	lastLine time.Time
	warnings []string
}

// Start launches spec's CLI. Folder trust for Claude Code is written first;
// a failure there is a warning, not an error. Nothing is left running when
// Start returns an error.
func (r *PTYRunner) Start(spec Spec) (Agent, error) {
	if err := checkSpec(spec); err != nil {
		return nil, err
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	debounce := r.Debounce
	if debounce == 0 {
		debounce = DefaultDebounce
	}

	launch := prepareLaunch(spec)
	envTimeout := r.EnvTimeout
	if envTimeout <= 0 {
		envTimeout = session.DefaultCommandTimeout
	}
	env, err := buildEnv(spec, launch.Env, runtime.GOOS == "windows", envTimeout)
	if err != nil {
		return nil, fmt.Errorf("start agent %s: %w", spec.Name, err)
	}
	termID, err := newTerminalID()
	if err != nil {
		return nil, fmt.Errorf("start agent %s: %w", spec.Name, err)
	}
	a := &ptyAgent{id: spec.SessionID, termID: termID, started: now(), delivery: make(chan struct{})}
	if spec.CLI == api.CLIClaude && spec.Dir != "" {
		// Claude Code asks whether to trust a new folder before it reads
		// anything; nobody is there to answer for an agent.
		if err := claudetrust.EnsureForEnv(effectiveEnv(env), spec.Dir); err != nil {
			a.warn(fmt.Sprintf("could not trust %s for Claude Code: %v", spec.Dir, err))
		}
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o700); err != nil {
		return nil, fmt.Errorf("start agent %s: create log folder: %w", spec.Name, err)
	}

	out := newOutputPipe()
	sess, err := session.Start(session.Options{
		Cmd:          spec.Command,
		Args:         launch.Args,
		Env:          env,
		Dir:          spec.Dir,
		LogPath:      spec.LogPath,
		LogMaxSizeKB: LogMaxSizeKB,
		LogKeep:      LogKeep,
		OnLogLine: func(string) {
			t := now()
			a.mu.Lock()
			a.lastLine = t
			a.mu.Unlock()
		},
		OnOutput: out.write,
		// The agent may print its environment; nothing omo stores or shows
		// to others may carry the id.
		Secret: spec.SessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("start agent %s: %w", spec.Name, err)
	}
	a.sess = sess
	go func() {
		<-sess.Done()
		out.end()
	}()

	input := &inputClock{}
	a.term = webterm.Own(termID, &terminalAdapter{sess: sess, out: out, input: input, now: now, kill: a.Kill}, nil)
	a.notes = newNotifier(sess, input, now, debounce)
	go a.notes.run()
	go func() {
		defer close(a.delivery)
		deliverStartPrompt(sess, spec.StartPrompt, launch.PromptInjected, spec.Delivery, spec.AwaitingReady, a.LastOutput, now, a.warn)
	}()
	return a, nil
}

// newTerminalID returns a random id for an agent's web terminal.
func newTerminalID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("create terminal id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// prepareLaunch applies the CLI's start adapter: how the start prompt and
// folder trust reach it. The environment passed in is the profile's and the
// model's, so the adapter sees what the user configured (TERM for one).
func prepareLaunch(spec Spec) agentcli.Launch {
	env := map[string]string{}
	for key, value := range spec.ProfileEnv {
		env[key] = value
	}
	for key, value := range spec.ModelEnv {
		env[key] = value
	}
	switch spec.CLI {
	case api.CLIClaude, api.CLICodex, api.CLIGemini:
		return agentcli.Prepare(agentcli.Provider(spec.CLI), spec.Command, spec.Args, env, spec.Dir, spec.StartPrompt, true, spec.Delivery.Inject)
	}
	// A custom CLI gets no adapter, even if its command is named like a
	// supported one: only %prompt% is replaced; otherwise the prompt is typed.
	launch := agentcli.Launch{Args: append([]string(nil), spec.Args...), Env: env}
	for i, arg := range launch.Args {
		if strings.Contains(arg, "%prompt%") {
			launch.Args[i] = strings.ReplaceAll(arg, "%prompt%", spec.StartPrompt)
			launch.PromptInjected = true
		}
	}
	return launch
}

func (a *ptyAgent) warn(message string) {
	a.mu.Lock()
	a.warnings = append(a.warnings, message)
	a.mu.Unlock()
}

func (a *ptyAgent) SessionID() string            { return a.id }
func (a *ptyAgent) TerminalID() string           { return a.termID }
func (a *ptyAgent) Done() <-chan struct{}        { return a.sess.Done() }
func (a *ptyAgent) ExitErr() error               { return a.sess.ExitErr() }
func (a *ptyAgent) SendPrompt(text string) error { return a.sess.SendPrompt(text) }
func (a *ptyAgent) Notify(line string)           { a.notes.enqueue(line) }
func (a *ptyAgent) Terminal() *webterm.Terminal  { return a.term }

// Kill ends the process tree. Killing an agent that already ended does
// nothing (its process id may belong to somebody else by now).
func (a *ptyAgent) Kill() error {
	select {
	case <-a.sess.Done():
		return nil
	default:
	}
	return a.sess.Kill()
}

func (a *ptyAgent) LastOutput() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastLine.IsZero() {
		return a.started
	}
	return a.lastLine
}

func (a *ptyAgent) Tail(n int) []string {
	lines, err := a.sess.TailLog(n)
	if err != nil {
		return nil
	}
	return lines
}

func (a *ptyAgent) Warnings() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.warnings...)
}
