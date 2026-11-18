// Package fakeagent is a scripted stand-in for an agent CLI. omo starts it as
// a command in a terminal, hands it a start prompt like it would a real CLI,
// and the fake then acts through the same client a real agent uses (`omo ai`
// is a thin layer over it), reads lines typed into its terminal as
// notifications, and exits or keeps running as its script says.
//
// Tests run it as their own test binary, re-executed with OMO_FAKEAGENT=1:
//
//	func TestMain(m *testing.M) {
//		if fakeagent.Enabled() {
//			os.Exit(fakeagent.Main())
//		}
//		os.Exit(m.Run())
//	}
//
// No model, no network beyond the local omo socket and no user configuration
// is involved. The session id (OMO_AGENT_ID) is a credential: it is never
// printed, anything that would contain it is replaced.
//
// A script is one op per line, fields separated by "|", "#" starts a comment
// line. The last field of an op that takes free text may contain "|". The
// placeholders $TICKET, $STORY, $PROJECT (the agent's own project: a
// reviewer's or an export job's, else the ticket's first), $OTHERS (the
// ticket's other projects, joined by ", "), $OUTPUT (the last line the
// last sh-block printed) and $NAME are replaced in every field. See Ops for
// the list.
package fakeagent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/mcpbridge"
)

// Environment variables the fake agent reads.
const (
	// EnvEnable is "1" in a process that must run as the fake agent.
	EnvEnable = "OMO_FAKEAGENT"
	// EnvScript holds a whole script and wins over everything else.
	EnvScript = "OMO_FAKE_SCRIPT"
	// EnvScriptRolePrefix plus the upper-case role (CEO, PM, DEVELOPER,
	// REVIEWER, EXPORT) holds the script of that role.
	EnvScriptRolePrefix = "OMO_FAKE_SCRIPT_"
	envAgentID          = "OMO_AGENT_ID"
	// EnvBridge is "1" in a process the fake agent started as omo's MCP
	// bridge (`omo mcp stdio`): its op mcp runs its own executable so.
	EnvBridge = "OMO_FAKEAGENT_MCP_BRIDGE"
)

// Exit codes of Run besides the ones a script asks for.
const (
	// ExitFailed is returned when an op fails.
	ExitFailed = 1
	// ExitBadScript is returned, before any call is made, when the script
	// does not parse.
	ExitBadScript = 2
)

// Enabled reports whether this process must run as the fake agent.
func Enabled() bool { return os.Getenv(EnvEnable) == "1" }

// Main runs the fake agent as a process: arguments, standard input and
// output, working directory and environment are the process's own.
// With EnvSwallowEnters or EnvDeafFor set it reads its terminal like a
// full-screen CLI with an input line (see composer).
func Main() int {
	if err := recordPID(); err != nil {
		fmt.Fprintf(os.Stdout, "fake: %v\n", err)
		return ExitFailed
	}
	if os.Getenv(EnvBridge) == "1" {
		return bridgeMain()
	}
	var in io.Reader = os.Stdin
	if swallow, deaf, paste, ok := composerSettings(); ok {
		if err := rawInput(os.Stdin); err != nil {
			fmt.Fprintf(os.Stdout, "fake: %v\n", err)
			return ExitFailed
		}
		drop, _ := strconv.Atoi(os.Getenv(EnvDropPrompts))
		in = pasteComposer(os.Stdin, os.Stdout, swallow, time.Now().Add(deaf), max(drop, 0), paste)
		if path := os.Getenv(EnvRawReadyFile); path != "" {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				fmt.Fprintf(os.Stdout, "fake: %v\n", err)
				return ExitFailed
			}
		}
	}
	return Run(context.Background(), Options{Args: os.Args[1:], Stdin: in, Stdout: os.Stdout})
}

// bridgeMain runs the process as `omo mcp stdio` does.
func bridgeMain() int {
	session, dial, err := mcpbridge.Environment()
	if err == nil {
		err = mcpbridge.Run(context.Background(), mcpbridge.Options{Dial: dial, Session: session, In: os.Stdin, Out: os.Stdout,
			Log: log.New(os.Stderr, "fake bridge: ", 0), Version: "fake"})
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake bridge: %v\n", err)
		return ExitFailed
	}
	return 0
}

// Env returns the environment variables that make a process run script as the
// fake agent. Add them to the environment of the agent profile.
func Env(script string) map[string]string {
	return map[string]string{EnvEnable: "1", EnvScript: script}
}

// EnvForRole is like Env for the script of one role only; other roles keep
// their built-in script.
func EnvForRole(role api.Role, script string) map[string]string {
	return map[string]string{EnvEnable: "1", EnvScriptRolePrefix + strings.ToUpper(string(role)): script}
}

// Command returns the command a profile runs to start the fake agent: the
// running executable. The caller sets Env (or EnvForRole) in the environment.
// Run as the test binary, it needs the TestMain hook shown in the package
// documentation; the command line may carry anything, the fake ignores
// -test.* flags.
func Command() (string, error) { return os.Executable() }

// Options configure Run. The zero value is a quiet agent without input in the
// current directory; Main fills in the process's own.
type Options struct {
	// Script overrides the script lookup of the environment.
	Script string
	// Args are the command line arguments of the agent (without the
	// program name); a start prompt may arrive in them.
	Args []string
	// Stdin is the terminal input: the typed start prompt and notifications.
	// Nil means no input.
	Stdin io.Reader
	// Stdout receives everything the agent prints (it is the transcript).
	// Nil discards.
	Stdout io.Writer
	// Dir is the working directory of write and commit; empty is ".".
	Dir string
}

// Run executes the script and returns the exit code. It returns early with 1
// when ctx ends. The client comes from OMO_SOCKET and OMO_AGENT_ID like a
// real agent's; it is only created when an op needs the server.
func Run(ctx context.Context, o Options) int {
	r := &runner{o: o, readyIdx: -1}
	if o.Dir == "" {
		r.o.Dir = "."
	}
	r.out = &redactor{w: o.Stdout, secret: strings.TrimSpace(os.Getenv(envAgentID))}
	if o.Stdin != nil {
		r.lines = newLineQueue(o.Stdin, r.say)
	}
	return r.run(ctx)
}

type runner struct {
	o   Options
	out *redactor

	steps    []Step
	readyIdx int

	lines *lineQueue

	client  *apiclient.Client
	me      *api.Me
	story   *int
	project string
	name    string
	prompt  string // last answer of ready
	reason  string // last answer of wait
	output  string // last line the last sh-block printed
	others  string // the ticket's other projects
	created []int  // the tickets create-ticket made
}

// say prints one line to the transcript.
func (r *runner) say(format string, args ...any) {
	fmt.Fprintf(r.out, format+"\n", args...)
}

func (r *runner) run(ctx context.Context) int {
	text := strings.TrimSpace(r.o.Script)
	if text == "" {
		text = strings.TrimSpace(os.Getenv(EnvScript))
	}
	if text == "" {
		var err error
		if text, err = r.roleScript(ctx); err != nil {
			r.say("fake: %v", err)
			return ExitFailed
		}
	}
	steps, err := Parse(text)
	if err != nil {
		r.say("fake: %v", err)
		return ExitBadScript
	}
	r.steps = steps
	for _, s := range steps {
		if s.Op == "ready" {
			r.readyIdx = s.Index
			break
		}
	}
	err = r.runRange(ctx, 0, len(steps))
	var ex exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ex):
		return ex.code
	case ctx.Err() != nil:
		r.say("fake: interrupted")
		return ExitFailed
	default:
		return ExitFailed
	}
}

// runRange executes steps[from:to].
func (r *runner) runRange(ctx context.Context, from, to int) error {
	for i := from; i < to; i++ {
		s := r.steps[i]
		if err := ctx.Err(); err != nil {
			return err
		}
		args, err := r.expand(ctx, s.Args)
		if err != nil {
			return r.fail(s, err)
		}
		s.Args = args
		if err := ops[s.Op].run(ctx, r, s); err != nil {
			var ex exitError
			if errors.As(err, &ex) || ctx.Err() != nil {
				return err
			}
			return r.fail(s, err)
		}
	}
	return nil
}

func (r *runner) fail(s Step, err error) error {
	r.say("fake: line %d (%s) failed: %v", s.Line, s.Op, err)
	return err
}

type exitError struct{ code int }

func (e exitError) Error() string { return "exit " + strconv.Itoa(e.code) }

// Step is one parsed script line.
type Step struct {
	Index int // position among the steps
	Line  int // line number in the script
	Op    string
	Args  []string
}

// Parse reads a script. Every line is checked (op, number of fields, numbers
// and durations) before anything runs, so a typo fails at once with its line
// number.
func Parse(text string) ([]Step, error) {
	var steps []Step
	for i, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, _ := strings.Cut(line, "|")
		name = strings.TrimSpace(name)
		spec, ok := ops[name]
		if !ok {
			return nil, fmt.Errorf("script line %d: unknown op %q", i+1, name)
		}
		var args []string
		if strings.Contains(line, "|") {
			if spec.max == 0 {
				args = strings.Split(rest, "|")
			} else {
				args = strings.SplitN(rest, "|", spec.max)
			}
		}
		if len(args) < spec.min || len(args) > spec.max {
			return nil, fmt.Errorf("script line %d: op %s takes %s", i+1, name, spec.usage)
		}
		if spec.check != nil {
			if err := spec.check(args); err != nil {
				return nil, fmt.Errorf("script line %d: op %s: %w", i+1, name, err)
			}
		}
		steps = append(steps, Step{Index: len(steps), Line: i + 1, Op: name, Args: args})
	}
	return steps, nil
}

// roleScript picks the script of the caller's role: the environment's, else
// the built-in default.
func (r *runner) roleScript(ctx context.Context) (string, error) {
	me, err := r.whoami(ctx)
	if err != nil {
		return "", err
	}
	role := me.Actor.Role
	if role == api.RoleDeveloper {
		t, err := r.ownTicket(ctx)
		if err == nil && t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageExporting {
			role = "export"
		}
	}
	if s := strings.TrimSpace(os.Getenv(EnvScriptRolePrefix + strings.ToUpper(string(role)))); s != "" {
		return s, nil
	}
	s, ok := defaults[string(role)]
	if !ok {
		return "", fmt.Errorf("no script for role %q: set %s", role, EnvScript)
	}
	return s, nil
}

// defaults are the built-in scripts per role.
var defaults = map[string]string{
	"developer": `ready
write|fake-$TICKET.txt|done by $NAME
commit|feat: fake work for #$TICKET
done|fake work
wait-loop|10m`,
	"pm": `ready
create-tasks|2|chain
release-all
until-tasks-done|10m
done|story complete
wait-loop|10m`,
	"reviewer": `ready
verdict|approve|looks good
wait-loop|10m`,
	"export": `ready
done-url|https://example.invalid/you/app/pull/$TICKET
wait|10m`,
	"ceo": `ready
hang`,
}

// whoami fetches and caches the caller's identity.
func (r *runner) whoami(ctx context.Context) (api.Me, error) {
	if r.me != nil {
		return *r.me, nil
	}
	c, err := r.api()
	if err != nil {
		return api.Me{}, err
	}
	me, err := c.Me(ctx)
	if err != nil {
		return api.Me{}, err
	}
	r.me = &me
	r.name = me.Actor.Name
	return me, nil
}

// api returns the client, created from the environment on first use.
func (r *runner) api() (*apiclient.Client, error) {
	if r.client != nil {
		return r.client, nil
	}
	c, err := apiclient.FromEnvironment()
	if err != nil {
		return nil, err
	}
	r.client = c
	return c, nil
}

// ownTicket fetches the ticket the session is bound to (not cached: its
// column changes).
func (r *runner) ownTicket(ctx context.Context) (api.Ticket, error) {
	me, err := r.whoami(ctx)
	if err != nil {
		return api.Ticket{}, err
	}
	if me.Ticket == nil {
		return api.Ticket{}, errors.New("this session has no ticket")
	}
	c, err := r.api()
	if err != nil {
		return api.Ticket{}, err
	}
	t, err := c.Ticket(ctx, *me.Ticket)
	if err != nil {
		return api.Ticket{}, err
	}
	r.story = t.Story
	if t.Type == api.TypeStory {
		n := t.Number
		r.story = &n
	}
	// $PROJECT is the agent's own project (a reviewer's, an export job's),
	// else the ticket's first.
	r.project = ""
	if len(t.Projects) > 0 {
		r.project = t.Projects[0].Project
	}
	for _, a := range t.Agents {
		if a.Name == me.Actor.Name && a.Project != "" {
			r.project = a.Project
		}
	}
	var others []string
	for _, p := range t.Projects {
		if p.Project != r.project {
			others = append(others, p.Project)
		}
	}
	r.others = strings.Join(others, ", ")
	return t, nil
}

// expand replaces the placeholders in args.
func (r *runner) expand(ctx context.Context, args []string) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		if !strings.Contains(a, "$") {
			out[i] = a
			continue
		}
		if strings.Contains(a, "$NAME") {
			if _, err := r.whoami(ctx); err != nil {
				return nil, err
			}
			a = strings.ReplaceAll(a, "$NAME", r.name)
		}
		a = strings.ReplaceAll(a, "$OUTPUT", r.output)
		if strings.Contains(a, "$TICKET") || strings.Contains(a, "$STORY") || strings.Contains(a, "$PROJECT") || strings.Contains(a, "$OTHERS") {
			t, err := r.ownTicket(ctx)
			if err != nil {
				return nil, err
			}
			a = strings.ReplaceAll(a, "$TICKET", strconv.Itoa(t.Number))
			a = strings.ReplaceAll(a, "$PROJECT", r.project)
			a = strings.ReplaceAll(a, "$OTHERS", r.others)
			story := ""
			if r.story != nil {
				story = strconv.Itoa(*r.story)
			}
			a = strings.ReplaceAll(a, "$STORY", story)
		}
		out[i] = a
	}
	return out, nil
}

// redactor writes to w and replaces the secret by a marker.
type redactor struct {
	mu     sync.Mutex
	w      io.Writer
	secret string
}

func (r *redactor) Write(p []byte) (int, error) {
	if r.w == nil {
		return len(p), nil
	}
	out := p
	if r.secret != "" {
		out = bytes.ReplaceAll(p, []byte(r.secret), []byte("[session id]"))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// lineQueue collects the lines typed into the agent's terminal.
type lineQueue struct {
	mu     sync.Mutex
	lines  []string
	closed bool
	wake   chan struct{}
}

// newLineQueue starts reading in and echoes every line through say as a
// notification the agent received. Lines nobody awaits are simply ignored.
func newLineQueue(in io.Reader, say func(string, ...any)) *lineQueue {
	q := &lineQueue{wake: make(chan struct{}, 1)}
	go func() {
		br := bufio.NewReader(in)
		for {
			line, err := br.ReadString('\n')
			if line = strings.TrimRight(line, "\r\n"); line != "" {
				say("fake: typed: %s", line)
				q.mu.Lock()
				q.lines = append(q.lines, line)
				q.mu.Unlock()
				q.signal()
			}
			if err != nil {
				q.mu.Lock()
				q.closed = true
				q.mu.Unlock()
				q.signal()
				return
			}
		}
	}()
	return q
}

func (q *lineQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// next returns the first typed line for which match is true, dropping the
// lines before it. It fails when ctx ends or the input is closed.
func (q *lineQueue) next(ctx context.Context, match func(string) bool) (string, error) {
	for {
		q.mu.Lock()
		for len(q.lines) > 0 {
			l := q.lines[0]
			q.lines = q.lines[1:]
			if match(l) {
				q.mu.Unlock()
				return l, nil
			}
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return "", errors.New("terminal input closed")
		}
		select {
		case <-q.wake:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
