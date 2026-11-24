// Package agents runs agent CLIs (Claude Code, Codex, Antigravity, Gemini or a custom
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
	// acknowledged the prompt. Only a typed prompt is resent, and only
	// while the terminal has shown no output since it was last typed and
	// the prompt's text is not on the screen.
	RetryWait time.Duration
	// Retries is the number of resends after the first delivery.
	Retries int
	// EchoWait is the longest omo waits, after typing the prompt, for its
	// text to show on the screen before it presses Enter.
	EchoWait time.Duration
	// SubmitCheck is the pause after an Enter before omo looks whether the
	// typed prompt is still sitting where it was; SubmitRetries is how
	// often it then presses Enter again (never retyping the text).
	SubmitCheck   time.Duration
	SubmitRetries int
}

// Default start-prompt delivery of every CLI type.
const (
	DefaultDelay         = 3 * time.Second
	DefaultRetryWait     = 30 * time.Second
	DefaultRetries       = 3
	DefaultEchoWait      = 5 * time.Second
	DefaultSubmitCheck   = 3 * time.Second
	DefaultSubmitRetries = 3
)

// ResolveDelivery fills the fields a profile leaves unset with the defaults:
// inject on, 3s delay, 3 retries every 30s, and the fixed checks that the
// typed prompt was submitted. Negative values count as zero.
func ResolveDelivery(p api.PromptDelivery) Delivery {
	d := Delivery{Inject: true, Delay: DefaultDelay, RetryWait: DefaultRetryWait, Retries: DefaultRetries,
		EchoWait: DefaultEchoWait, SubmitCheck: DefaultSubmitCheck, SubmitRetries: DefaultSubmitRetries}
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
	// Name, Command, Args, Dir, LogPath, StartPrompt or MCPBridge; Start
	// refuses such a spec.
	SessionID string
	// Name is the agent's display name, used in error messages.
	Name string
	// CLI is the profile's CLI type (api.CLIClaude, CLICodex, CLIGemini, CLIAgy or
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
	// MCPBridge is the absolute path of the omo executable whose `mcp
	// stdio` a Claude Code or Codex agent gets as its MCP server "omo" for
	// this session (agentcli.BridgeArgs); "" gives none. The bridge learns
	// the session from OMO_AGENT_ID, never from an argument.
	MCPBridge string
	// AwaitingReady reports whether the agent has not yet acknowledged its
	// start prompt. Typed deliveries and resends happen only while it
	// returns true; nil means "always". It must return quickly.
	AwaitingReady func() bool
	// OnWarning, when set, gets each problem found after Start returned
	// that did not stop the agent (a start prompt that could not be
	// typed), from another goroutine; it must not block for long.
	// Warnings lists only what was found before Start returned.
	OnWarning func(message string)
}

// Agent is one running agent process.
type Agent interface {
	// SessionID is the id the engine put in the spec. It is the only
	// accessor that returns it.
	SessionID() string
	// TerminalID is a random id of the agent's web terminal, unrelated to
	// the session id; it is also the terminal's Info().ID. Use it wherever
	// a terminal is named to a viewer.
	TerminalID() string
	// Exited is closed once the agent's main process has exited. A child
	// it left running in the background may still hold the terminal open,
	// so Done can stay open after that. The engine should treat Exited as
	// the end of the agent: then call Kill, which ends whatever the
	// process left behind, and wait for Done before cleaning up.
	Exited() <-chan struct{}
	// Done is closed once the terminal reached end of file and its output
	// is drained: the main process and every child holding the terminal
	// are gone, and nothing the agent kept for its process (the record in
	// the run folder) is left. The registry drops the agent at Done.
	Done() <-chan struct{}
	// ExitErr is the main process's exit status once Exited is closed
	// (nil for exit 0).
	ExitErr() error
	// SendPrompt types text into the terminal and submits it.
	SendPrompt(text string) error
	// Notify queues one line to be typed and submitted once nobody has
	// typed into the terminal for the hold-back period. The line is made
	// safe first:
	// one line without control characters, prefixed with NotifyPrefix
	// (pass the message without it; an existing prefix is not doubled),
	// at most MaxNotifyLength characters. Lines keep their order; lines
	// still queued when the process exits are dropped.
	Notify(line string)
	// NotifyDeferred renders a notification when it can actually be typed,
	// including after waiting for the terminal input lock. An empty result
	// drops it. render must be safe to call repeatedly and return promptly.
	NotifyDeferred(render func() string)
	// Kill ends the process and every process it started, also after the
	// main process exited on its own.
	Kill() error
	// LastOutput is when the transcript last gained a line (the start time
	// before the first one). Spinners and redraws do not count.
	LastOutput() time.Time
	// Tail returns the last n transcript lines.
	Tail(n int) []string
	// Terminal is the agent's web terminal.
	Terminal() *webterm.Terminal
	// Warnings lists problems that did not stop the agent and were found
	// while it started, such as a folder trust entry that could not be
	// written. Later ones go to Spec.OnWarning.
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
	fields := append([]string{spec.Name, spec.Command, spec.Dir, spec.LogPath, spec.StartPrompt, spec.AccountDir, spec.MCPBridge}, spec.Args...)
	for _, field := range fields {
		if strings.Contains(field, spec.SessionID) {
			return fmt.Errorf("start agent %s: %w", spec.Name, ErrSessionIDExposed)
		}
	}
	return nil
}

// Registry maps session ids to their running agents and terminal ids to
// session ids. An agent leaves the registry by itself when its process ends.
type Registry struct {
	mu        sync.Mutex
	agents    map[string]Agent
	terminals map[string]string // terminal id -> session id
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{agents: map[string]Agent{}, terminals: map[string]string{}}
}

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
	r.terminals[a.TerminalID()] = a.SessionID()
	r.mu.Unlock()
	go func() {
		<-a.Done()
		r.mu.Lock()
		if r.agents[a.SessionID()] == a {
			delete(r.agents, a.SessionID())
			delete(r.terminals, a.TerminalID())
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
