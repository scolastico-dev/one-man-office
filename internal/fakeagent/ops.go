package fakeagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// opSpec describes one script op: the number of fields after the op name, a
// parse-time check and the implementation. When max fields are allowed the
// last one takes the rest of the line, "|" included.
type opSpec struct {
	min, max int
	usage    string
	check    func(args []string) error
	run      func(ctx context.Context, r *runner, s Step) error
}

// ops is the table of script ops. It is filled in init because several ops
// run other steps.
var ops map[string]opSpec

// Ops lists the op names with their usage, for documentation and tests.
func Ops() map[string]string {
	out := make(map[string]string, len(ops))
	for n, s := range ops {
		out[n] = s.usage
	}
	return out
}

func init() {
	ops = map[string]opSpec{
		// What a real agent does.
		"await-prompt": {0, 1, "[arg|typed]", oneOf(0, "any", "arg", "typed"), opAwaitPrompt},
		"ready":        {0, 0, "no fields", nil, opReady},
		"save-prompt":  {1, 1, "PATH", nil, opSavePrompt},
		"write":        {1, 2, "FILE[|TEXT]", nil, opWrite},
		"commit":       {1, 1, "MESSAGE", nil, opCommit},
		"done":         {0, 1, "[TEXT]", nil, opDone},
		"verdict":      {1, 2, "approve|reject[|TEXT]", oneOf(0, "approve", "reject"), opVerdict},
		"done-url":     {1, 1, "URL", nil, opDoneURL},
		"done-failed":  {1, 1, "TEXT", nil, opDoneFailed},
		"comment":      {2, 2, "TICKET|TEXT", nil, opComment},
		"ask":          {1, 1, "TEXT", nil, opAsk},
		"wait":         {1, 1, "DURATION", duration(0), opWait},
		// A pm.
		"create-tasks":     {1, 2, "COUNT[|chain]", checkCreateTasks, opCreateTasks},
		"release-all":      {0, 0, "no fields", nil, opReleaseAll},
		"until-tasks-done": {1, 1, "DURATION", duration(0), opUntilTasksDone},
		// Waiting and handing over.
		"handoff-on-stop": {0, 1, "[DURATION]", optDuration(0), opHandoffOnStop},
		"wait-loop":       {1, 1, "DURATION", duration(0), opWaitLoop},
		// Misbehaviour and plumbing.
		"hang":       {0, 0, "no fields", nil, opHang},
		"crash":      {1, 1, "CODE", integer(0), opCrash},
		"exit":       {1, 1, "CODE", integer(0), opExit},
		"sleep":      {1, 1, "DURATION", duration(0), opSleep},
		"print":      {1, 1, "TEXT", nil, opPrint},
		"flood":      {0, 1, "[LINES]", optInteger(0), opFlood},
		"await-line": {1, 2, "TEXT[|DURATION]", optDuration(1), opAwaitLine},
	}
}

func oneOf(i int, allowed ...string) func([]string) error {
	return func(args []string) error {
		if i >= len(args) {
			return nil
		}
		for _, a := range allowed {
			if args[i] == a {
				return nil
			}
		}
		return fmt.Errorf("%q is not one of %s", args[i], strings.Join(allowed, ", "))
	}
}

func duration(i int) func([]string) error {
	return func(args []string) error {
		d, err := time.ParseDuration(args[i])
		if err != nil || d <= 0 {
			return fmt.Errorf("%q is not a positive duration", args[i])
		}
		return nil
	}
}

func optDuration(i int) func([]string) error {
	return func(args []string) error {
		if i >= len(args) || args[i] == "" {
			return nil
		}
		return duration(i)(args)
	}
}

func integer(i int) func([]string) error {
	return func(args []string) error {
		if _, err := strconv.Atoi(args[i]); err != nil {
			return fmt.Errorf("%q is not a number", args[i])
		}
		return nil
	}
}

func optInteger(i int) func([]string) error {
	return func(args []string) error {
		if i >= len(args) || args[i] == "" {
			return nil
		}
		return integer(i)(args)
	}
}

func checkCreateTasks(args []string) error {
	if n, err := strconv.Atoi(args[0]); err != nil || n < 1 {
		return fmt.Errorf("%q is not a positive number", args[0])
	}
	if len(args) == 2 && args[1] != "chain" {
		return fmt.Errorf("second field is %q, only chain is known", args[1])
	}
	return nil
}

func arg(s Step, i int) string {
	if i < len(s.Args) {
		return s.Args[i]
	}
	return ""
}

// ---- what a real agent does ----

// opAwaitPrompt waits for the start prompt. A CLI gets it in its command
// line (Codex: positional, Gemini: after --prompt-interactive, custom:
// substituted) or typed into its terminal (Claude); mode "arg" and "typed"
// insist on one of them, the default takes the command line first.
func opAwaitPrompt(ctx context.Context, r *runner, s Step) error {
	mode := arg(s, 0)
	if mode == "" {
		mode = "any"
	}
	if mode != "typed" {
		if p, how, ok := promptInArgs(r.o.Args); ok {
			r.say("fake: start prompt received as %s: %s", how, p)
			return nil
		}
		if mode == "arg" {
			return errors.New("the command line carries no start prompt")
		}
	}
	if r.lines == nil {
		return errors.New("no start prompt in the command line and no terminal input")
	}
	line, err := r.lines.next(ctx, func(l string) bool { return strings.Contains(l, "omo ai ready") })
	if err != nil {
		return fmt.Errorf("waiting for the typed start prompt: %w", err)
	}
	r.say("fake: start prompt received by typing: %s", line)
	return nil
}

// promptInArgs finds the start prompt among the command line arguments.
func promptInArgs(args []string) (prompt, how string, ok bool) {
	prev := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-test.") {
			continue
		}
		if strings.Contains(a, "omo ai ready") {
			switch {
			case strings.HasPrefix(a, "-") && strings.Contains(a, "="):
				flag, value, _ := strings.Cut(a, "=")
				return value, "the value of " + flag, true
			case strings.HasPrefix(prev, "-"):
				return a, "the value of " + prev, true
			default:
				return a, "an argument", true
			}
		}
		prev = a
	}
	return "", "", false
}

func opReady(ctx context.Context, r *runner, _ Step) error {
	c, err := r.api()
	if err != nil {
		return err
	}
	p, err := c.Ready(ctx)
	if err != nil {
		return err
	}
	r.prompt = p
	r.say("fake: ready, prompt follows:\n%s\nfake: end of prompt", p)
	return nil
}

func opSavePrompt(_ context.Context, r *runner, s Step) error {
	path := s.Args[0]
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.o.Dir, path)
	}
	return os.WriteFile(path, []byte(r.prompt), 0o644)
}

func opWrite(_ context.Context, r *runner, s Step) error {
	if !filepath.IsLocal(s.Args[0]) {
		return fmt.Errorf("%q is not a path inside the working directory", s.Args[0])
	}
	path := filepath.Join(r.o.Dir, s.Args[0])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(arg(s, 1)+"\n"), 0o644); err != nil {
		return err
	}
	r.say("fake: wrote %s", s.Args[0])
	return nil
}

// git runs git in the working directory without touching any configuration:
// a missing identity is given on the command line, user and system
// configuration are not read, hooks do not run.
func (r *runner) git(ctx context.Context, args ...string) error {
	full := append([]string{
		"-c", "user.name=fake agent", "-c", "user.email=fake-agent@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.o.Dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// opCommit stages everything and commits: the fake owns its worktree.
func opCommit(ctx context.Context, r *runner, s Step) error {
	if err := r.git(ctx, "add", "-A"); err != nil {
		return err
	}
	if err := r.git(ctx, "commit", "--no-verify", "-m", s.Args[0]); err != nil {
		return err
	}
	r.say("fake: committed: %s", s.Args[0])
	return nil
}

func (r *runner) hand(ctx context.Context, req api.DoneRequest, what string) error {
	c, err := r.api()
	if err != nil {
		return err
	}
	t, err := c.Done(ctx, req)
	if err != nil {
		return err
	}
	r.say("fake: %s #%d, now in %s", what, t.Number, t.Column)
	return nil
}

func opDone(ctx context.Context, r *runner, s Step) error {
	return r.hand(ctx, api.DoneRequest{Comment: arg(s, 0)}, "handed in")
}

func opVerdict(ctx context.Context, r *runner, s Step) error {
	return r.hand(ctx, api.DoneRequest{Verdict: s.Args[0], Comment: arg(s, 1)}, "verdict "+s.Args[0]+" on")
}

func opDoneURL(ctx context.Context, r *runner, s Step) error {
	return r.hand(ctx, api.DoneRequest{URL: s.Args[0]}, "reported "+s.Args[0]+" for")
}

func opDoneFailed(ctx context.Context, r *runner, s Step) error {
	return r.hand(ctx, api.DoneRequest{Error: s.Args[0]}, "reported a failure for")
}

func (r *runner) comment(ctx context.Context, n int, body string, question bool) error {
	c, err := r.api()
	if err != nil {
		return err
	}
	res, err := c.Comment(ctx, n, api.CommentRequest{Body: body, Question: question})
	if err != nil {
		return err
	}
	kind := "comment"
	if question {
		kind = "question"
	}
	r.say("fake: %s %d posted on #%d", kind, res.Comment.ID, n)
	return nil
}

func opComment(ctx context.Context, r *runner, s Step) error {
	n, err := strconv.Atoi(strings.TrimSpace(s.Args[0]))
	if err != nil {
		return fmt.Errorf("%q is not a ticket number", s.Args[0])
	}
	return r.comment(ctx, n, s.Args[1], false)
}

func opAsk(ctx context.Context, r *runner, s Step) error {
	t, err := r.ownTicket(ctx)
	if err != nil {
		return err
	}
	return r.comment(ctx, t.Number, s.Args[0], true)
}

func (r *runner) wait(ctx context.Context, d string) error {
	c, err := r.api()
	if err != nil {
		return err
	}
	reason, err := c.Wait(ctx, d)
	if err != nil {
		return err
	}
	r.reason = reason
	r.say("fake: woke up: %s", reason)
	return nil
}

func opWait(ctx context.Context, r *runner, s Step) error { return r.wait(ctx, s.Args[0]) }

// ---- a pm ----

func opCreateTasks(ctx context.Context, r *runner, s Step) error {
	count, _ := strconv.Atoi(s.Args[0])
	if _, err := r.ownTicket(ctx); err != nil {
		return err
	}
	if r.story == nil {
		return errors.New("the own ticket is not a story and belongs to none")
	}
	c, err := r.api()
	if err != nil {
		return err
	}
	var prev int
	for i := 1; i <= count; i++ {
		t, err := c.CreateTicket(ctx, api.CreateTicket{
			Type:     api.TypeTask,
			Title:    fmt.Sprintf("task %d", i),
			Projects: []string{r.project},
			Story:    r.story,
		})
		if err != nil {
			return err
		}
		r.say("fake: created #%d task %d", t.Number, i)
		if arg(s, 1) == "chain" && prev != 0 {
			if _, err := c.AddDependency(ctx, t.Number, prev); err != nil {
				return err
			}
			r.say("fake: #%d depends on #%d", t.Number, prev)
		}
		prev = t.Number
	}
	return nil
}

func opReleaseAll(ctx context.Context, r *runner, _ Step) error {
	if _, err := r.ownTicket(ctx); err != nil {
		return err
	}
	c, err := r.api()
	if err != nil {
		return err
	}
	list, err := c.Tickets(ctx, api.TicketFilter{Story: r.story, Type: api.TypeTask, Column: api.ColumnPlanning})
	if err != nil {
		return err
	}
	for _, t := range list.Tickets {
		if _, err := c.Move(ctx, t.Number, api.MoveRequest{Column: api.ColumnReady}); err != nil {
			return err
		}
		r.say("fake: released #%d", t.Number)
	}
	return nil
}

// openTasks counts the tasks of the story that are neither done nor
// cancelled.
func (r *runner) openTasks(ctx context.Context) (int, error) {
	c, err := r.api()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, col := range []api.Column{api.ColumnPlanning, api.ColumnReady, api.ColumnInProgress, api.ColumnReview} {
		list, err := c.Tickets(ctx, api.TicketFilter{Story: r.story, Type: api.TypeTask, Column: col})
		if err != nil {
			return 0, err
		}
		n += len(list.Tickets)
	}
	return n, nil
}

func opUntilTasksDone(ctx context.Context, r *runner, s Step) error {
	if _, err := r.ownTicket(ctx); err != nil {
		return err
	}
	for {
		open, err := r.openTasks(ctx)
		if err != nil {
			return err
		}
		if open == 0 {
			r.say("fake: every task of the story is done or cancelled")
			return nil
		}
		if err := r.wait(ctx, s.Args[0]); err != nil {
			return err
		}
	}
}

// ---- waiting and handing over ----

// opHandoffOnStop answers the request omo sends when it is stopping: when the
// last wait reason says so, it leaves a handoff comment and waits again.
func opHandoffOnStop(ctx context.Context, r *runner, s Step) error {
	if !strings.HasPrefix(r.reason, "omo: omo is stopping") {
		return nil
	}
	t, err := r.ownTicket(ctx)
	if err != nil {
		return err
	}
	if err := r.comment(ctx, t.Number, "Handoff: fake state", false); err != nil {
		return err
	}
	d := arg(s, 0)
	if d == "" {
		d = "10m"
	}
	return r.wait(ctx, d)
}

// opWaitLoop waits for ever. When it is woken while the own ticket is back In
// Progress (a rejection or a merge conflict), it does its work again: the
// steps between the first ready and itself run once more.
func opWaitLoop(ctx context.Context, r *runner, s Step) error {
	for {
		if err := r.wait(ctx, s.Args[0]); err != nil {
			return err
		}
		t, err := r.ownTicket(ctx)
		if err != nil {
			return err
		}
		if t.Column == api.ColumnInProgress {
			r.say("fake: #%d is back in progress, working again", t.Number)
			if err := r.runRange(ctx, r.readyIdx+1, s.Index); err != nil {
				return err
			}
		}
	}
}

// ---- misbehaviour and plumbing ----

func opHang(ctx context.Context, _ *runner, _ Step) error {
	<-ctx.Done()
	return ctx.Err()
}

func opCrash(_ context.Context, r *runner, s Step) error {
	code, _ := strconv.Atoi(s.Args[0])
	r.say("fake: crash with exit code %d", code)
	return exitError{code}
}

func opExit(_ context.Context, r *runner, s Step) error {
	code, _ := strconv.Atoi(s.Args[0])
	r.say("fake: exit %d", code)
	return exitError{code}
}

func opSleep(ctx context.Context, _ *runner, s Step) error {
	d, _ := time.ParseDuration(s.Args[0])
	return sleep(ctx, d)
}

func opPrint(_ context.Context, r *runner, s Step) error {
	r.say("%s", s.Args[0])
	return nil
}

// opFlood prints lines without pause: LINES of them, or until it is killed.
func opFlood(ctx context.Context, r *runner, s Step) error {
	limit, _ := strconv.Atoi(arg(s, 0))
	for i := 1; limit <= 0 || i <= limit; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(r.out, "fake: flood line %d\n", i); err != nil {
			return err
		}
	}
	return nil
}

// opAwaitLine waits for a typed line containing the text (a notification),
// at most the duration (default 30s).
func opAwaitLine(ctx context.Context, r *runner, s Step) error {
	if r.lines == nil {
		return errors.New("no terminal input")
	}
	d := 30 * time.Second
	if v := arg(s, 1); v != "" {
		d, _ = time.ParseDuration(v)
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	want := s.Args[0]
	if _, err := r.lines.next(ctx, func(l string) bool { return strings.Contains(l, want) }); err != nil {
		return fmt.Errorf("no typed line containing %q: %w", want, err)
	}
	return nil
}
