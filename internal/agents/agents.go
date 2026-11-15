// Package agents runs agent CLIs (Claude Code, Codex, Gemini or a custom
// command) in pseudo-terminals that omo owns itself. Each agent gets its
// environment, its folder trust and its start prompt the way its CLI
// expects them, a bounded transcript on disk, a web terminal any number of
// viewers can attach to, and a notification queue that never types into a
// line the user is composing.
//
// The session id of an agent is a credential: it is handed to the process
// only through OMO_AGENT_ID in its environment and never appears in a
// command-line argument, a log line or an error message of this package.
package agents

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/webterm"
)

// Delivery is the resolved start-prompt delivery of a profile.
type Delivery struct {
	// Inject enables automatic delivery: a launch argument where the CLI
	// takes one, otherwise typing the prompt into the terminal. With Inject
	// off only an explicit %prompt% argument delivers the prompt.
	Inject bool
	// Delay is how long omo waits before typing the prompt.
	Delay time.Duration
	// RetryWait is the pause between resends while the agent has not
	// acknowledged the prompt.
	RetryWait time.Duration
	// Retries is the number of resends after the first delivery.
	Retries int
}

// Default start-prompt delivery of every CLI type.
const (
	DefaultDelay     = 3 * time.Second
	DefaultRetryWait = 30 * time.Second
	DefaultRetries   = 3
)

// ResolveDelivery fills the fields a profile leaves unset with the defaults:
// inject on, 3s delay, 3 retries every 30s. Negative values count as zero.
func ResolveDelivery(p api.PromptDelivery) Delivery {
	d := Delivery{Inject: true, Delay: DefaultDelay, RetryWait: DefaultRetryWait, Retries: DefaultRetries}
	if p.Inject != nil {
		d.Inject = *p.Inject
	}
	if p.Delay != nil {
		d.Delay = max(time.Duration(*p.Delay), 0)
	}
	if p.RetryWait != nil {
		d.RetryWait = max(time.Duration(*p.RetryWait), 0)
	}
	if p.Retries != nil {
		d.Retries = max(*p.Retries, 0)
	}
	return d
}

// ReadyTimeout is how long an agent may take to acknowledge its start
// prompt: every delivery attempt plus a minute, and never less than two
// minutes.
func (d Delivery) ReadyTimeout() time.Duration {
	return max(2*time.Minute, d.Delay+time.Duration(d.Retries)*d.RetryWait+time.Minute)
}

// Spec describes one agent process.
type Spec struct {
	// SessionID becomes OMO_AGENT_ID and nothing else. It must not occur in
	// Name, Command, Args, Dir, LogPath or StartPrompt; Start refuses such a
	// spec.
	SessionID string
	// Name is the agent's display name, used in error messages.
	Name string
	// CLI is the profile's CLI type (api.CLIClaude, CLICodex, CLIGemini or
	// CLICustom). It selects prompt delivery, folder trust and the variable
	// AccountDir maps to.
	CLI     string
	Command string
	// Args are the profile's arguments followed by the model's.
	Args []string
	// AgentEnv is the shared agent environment; its values are expanded
	// (variable references, fallbacks, backtick commands).
	AgentEnv map[string]string
	// ProfileEnv and ModelEnv are literal and override AgentEnv, the model's
	// over the profile's.
	ProfileEnv map[string]string
	ModelEnv   map[string]string
	// AccountDir is the profile's account folder; see BuildEnv.
	AccountDir string
	// Dir is the working directory. For Claude Code it is also trusted.
	Dir string
	// LogPath is the transcript file; it rotates at 10 MiB, three old
	// segments are kept.
	LogPath string
	// Socket is the omo endpoint, exported as OMO_SOCKET.
	Socket string
	// PathPrefix is put in front of PATH (the folder of the omo executable).
	PathPrefix string
	// StartPrompt is the first prompt; it may end up as a launch argument.
	StartPrompt string
	Delivery    Delivery
	// AwaitingReady reports whether the agent has not yet acknowledged its
	// start prompt. Typed deliveries and resends happen only while it
	// returns true; nil means "always". It must return quickly.
	AwaitingReady func() bool
}

// Agent is one running agent process.
type Agent interface {
	SessionID() string
	// Done is closed once the process has exited and its output is drained.
	Done() <-chan struct{}
	// ExitErr is the process's exit status after Done (nil for exit 0).
	ExitErr() error
	// SendPrompt types text into the terminal and submits it.
	SendPrompt(text string) error
	// Notify queues one line to be typed and submitted once nobody has
	// typed into the terminal for the hold-back period. Lines keep their
	// order; lines still queued when the process exits are dropped.
	Notify(line string)
	// Kill ends the process and every process it started.
	Kill() error
	// LastOutput is when the transcript last gained a line (the start time
	// before the first one). Spinners and redraws do not count.
	LastOutput() time.Time
	// Tail returns the last n transcript lines.
	Tail(n int) []string
	// Terminal is the agent's web terminal.
	Terminal() *webterm.Terminal
	// Warnings lists problems that did not stop the agent, such as a folder
	// trust entry that could not be written.
	Warnings() []string
}

// Runner starts agent processes.
type Runner interface {
	Start(spec Spec) (Agent, error)
}

// ErrSessionIDExposed refuses a spec that would leak its session id.
var ErrSessionIDExposed = errors.New("the session id must only reach the agent through its environment")

// checkSpec validates a spec without ever naming the session id.
func checkSpec(spec Spec) error {
	if spec.SessionID == "" {
		return fmt.Errorf("start agent %s: no session id", spec.Name)
	}
	if spec.Command == "" {
		return fmt.Errorf("start agent %s: no command", spec.Name)
	}
	if spec.LogPath == "" {
		return fmt.Errorf("start agent %s: no log path", spec.Name)
	}
	fields := append([]string{spec.Name, spec.Command, spec.Dir, spec.LogPath, spec.StartPrompt, spec.AccountDir}, spec.Args...)
	for _, field := range fields {
		if strings.Contains(field, spec.SessionID) {
			return fmt.Errorf("start agent %s: %w", spec.Name, ErrSessionIDExposed)
		}
	}
	return nil
}

// Registry maps session ids to their running agents. An agent leaves the
// registry by itself when its process ends.
type Registry struct {
	mu     sync.Mutex
	agents map[string]Agent
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{agents: map[string]Agent{}} }

// ErrDuplicateSession is returned by Add for a session that already has a
// running agent.
var ErrDuplicateSession = errors.New("the session already has a running agent")

// Add records a running agent. An agent that already ended is not added.
func (r *Registry) Add(a Agent) error {
	r.mu.Lock()
	if _, ok := r.agents[a.SessionID()]; ok {
		r.mu.Unlock()
		return ErrDuplicateSession
	}
	select {
	case <-a.Done():
		r.mu.Unlock()
		return nil
	default:
	}
	r.agents[a.SessionID()] = a
	r.mu.Unlock()
	go func() {
		<-a.Done()
		r.mu.Lock()
		if r.agents[a.SessionID()] == a {
			delete(r.agents, a.SessionID())
		}
		r.mu.Unlock()
	}()
	return nil
}

// Get returns the running agent of a session.
func (r *Registry) Get(sessionID string) (Agent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.agents[sessionID]
	return a, ok
}

// Terminal returns the web terminal of a session's running agent.
func (r *Registry) Terminal(sessionID string) (*webterm.Terminal, bool) {
	a, ok := r.Get(sessionID)
	if !ok {
		return nil, false
	}
	return a.Terminal(), true
}

// All returns every running agent.
func (r *Registry) All() []Agent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	return out
}

// KillAll kills every running agent and waits up to timeout for all of
// them to end. It reports whether they all ended in time.
func (r *Registry) KillAll(timeout time.Duration) bool {
	all := r.All()
	for _, a := range all {
		_ = a.Kill()
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for _, a := range all {
		select {
		case <-a.Done():
		case <-deadline.C:
			return false
		}
	}
	return true
}
