package fakeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
		"append":       {1, 2, "FILE[|TEXT]", nil, opAppend},
		"commit":       {1, 1, "MESSAGE", nil, opCommit},
		"done":         {0, 1, "[TEXT]", nil, opDone},
		"verdict":      {1, 2, "approve|reject[|TEXT]", oneOf(0, "approve", "reject"), opVerdict},
		"verdicts":     {1, 2, "VERDICT,VERDICT...[|TEXT]", checkVerdicts, opVerdicts},
		"git":          {1, maxGitArgs, "ARG[|ARG...]", nil, opGit},
		"done-url":     {1, 1, "URL", nil, opDoneURL},
		"done-failed":  {1, 1, "TEXT", nil, opDoneFailed},
		"export-text":  {0, 0, "no fields", nil, opExportText},
		"sh-block":     {1, 1, "N", integer(0), opShBlock},
		"comment":      {2, 2, "TICKET|TEXT", nil, opComment},
		"image":        {2, 3, "TICKET|NAME[|TEXT]", nil, opImage},
		"ask":          {1, 1, "TEXT", nil, opAsk},
		"wait":         {1, 1, "DURATION", duration(0), opWait},
		"mcp":          {1, 2, "TOOL[|JSON]", checkJSONObject(1), opMCP},
		"mcp-tools":    {0, 0, "no fields", nil, opMCPTools},
		// A pm.
		"create-tasks":     {1, 2, "COUNT[|chain]", checkCreateTasks, opCreateTasks},
		"release-all":      {0, 0, "no fields", nil, opReleaseAll},
		"until-tasks-done": {1, 1, "DURATION", duration(0), opUntilTasksDone},
		// The CEO.
		"create-ticket":   {3, 3, "story|task|PROJECT|TITLE (PROJECT - for none)", oneOf(0, "story", "task"), opCreateTicket},
		"release-created": {0, 0, "no fields (moves what create-ticket made to ready, says refusals)", nil, opReleaseCreated},
		// Waiting and handing over.
		"handoff-on-stop": {0, 1, "[DURATION]", optDuration(0), opHandoffOnStop},
		"wait-loop":       {1, 2, "DURATION[|ANSWER]", duration(0), opWaitLoop},
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

// maxGitArgs is the most arguments the git op takes; the last one takes the
// rest of the line.
const maxGitArgs = 32

func checkVerdicts(args []string) error {
	for _, v := range strings.Split(args[0], ",") {
		if v != "approve" && v != "reject" {
			return fmt.Errorf("%q is not one of approve, reject", v)
		}
	}
	return nil
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
// line (Codex: positional, Gemini and Antigravity: after
// --prompt-interactive, custom: substituted) or typed into its terminal (Claude); mode "arg" and "typed"
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
	// A test that waits for this file to exist reads it right away: it must
	// never exist half-written, which os.WriteFile (create, then fill) allows.
	return writeFileAtomic(path, []byte(r.prompt), 0o644)
}

// beforeRename is called, in tests, once a temporary file holds all of its
// content and before it takes its final name.
var beforeRename func(final string)

// writeFileAtomic gives path its complete content in one step: the content is
// written to a temporary file in the same folder, which then takes the name.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), perm)
	}
	if err == nil {
		if beforeRename != nil {
			beforeRename(path)
		}
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
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

// opAppend adds TEXT and a line break to FILE in the working directory.
func opAppend(_ context.Context, r *runner, s Step) error {
	if !filepath.IsLocal(s.Args[0]) {
		return fmt.Errorf("%q is not a path inside the working directory", s.Args[0])
	}
	f, err := os.OpenFile(filepath.Join(r.o.Dir, s.Args[0]), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(arg(s, 1) + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		r.say("fake: appended to %s", s.Args[0])
	}
	return err
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

// opCommit stages everything and commits: the fake owns its worktree. A
// commit without changes is made all the same: a script that works again
// after a rejection repeats its steps, whose files are then unchanged.
func opCommit(ctx context.Context, r *runner, s Step) error {
	if err := r.git(ctx, "add", "-A"); err != nil {
		return err
	}
	if err := r.git(ctx, "commit", "--allow-empty", "--no-verify", "-m", s.Args[0]); err != nil {
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

// opVerdicts gives the verdict listed for the own ticket's review round: the
// first for round 1, the second for round 2, the last for every later one.
func opVerdicts(ctx context.Context, r *runner, s Step) error {
	t, err := r.ownTicket(ctx)
	if err != nil {
		return err
	}
	list := strings.Split(s.Args[0], ",")
	i := min(max(t.ReviewRound, 1), len(list)) - 1
	return r.hand(ctx, api.DoneRequest{Verdict: list[i], Comment: arg(s, 1)}, "verdict "+list[i]+" on")
}

// opGit runs git with the fields as arguments in the working directory, for
// what an agent does besides committing (merging the base branch into its
// own, for example).
func opGit(ctx context.Context, r *runner, s Step) error {
	if err := r.git(ctx, s.Args...); err != nil {
		return err
	}
	r.say("fake: ran git %s", strings.Join(s.Args, " "))
	return nil
}

func opDoneURL(ctx context.Context, r *runner, s Step) error {
	return r.hand(ctx, api.DoneRequest{URL: s.Args[0]}, "reported "+s.Args[0]+" for")
}

func opDoneFailed(ctx context.Context, r *runner, s Step) error {
	return r.hand(ctx, api.DoneRequest{Error: s.Args[0]}, "reported a failure for")
}

// opExportText has omo check the pull request text in title.txt and body.md,
// as `omo ai export-text` does.
func opExportText(ctx context.Context, r *runner, _ Step) error {
	c, err := r.api()
	if err != nil {
		return err
	}
	t, err := c.ExportText(ctx)
	if err != nil {
		return err
	}
	r.say("fake: export text: title from %s, body from %s, problems %q", t.TitleFrom, t.BodyFrom, t.Problems)
	return nil
}

// shBlock finds the code blocks of a prompt that hold a shell command.
var shBlock = regexp.MustCompile("```sh\n\\s*(.*)\n")

// opShBlock runs the command of the Nth ```sh block of the prompt that ready
// returned with sh in the working directory, as an agent runs a command its
// instructions give. The last line the command printed is $OUTPUT from then
// on.
func opShBlock(ctx context.Context, r *runner, s Step) error {
	n, _ := strconv.Atoi(s.Args[0])
	blocks := shBlock.FindAllStringSubmatch(r.prompt, -1)
	if n < 1 || n > len(blocks) {
		return fmt.Errorf("the prompt has %d shell blocks, not a block %d", len(blocks), n)
	}
	line := blocks[n-1][1]
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	cmd.Dir = r.o.Dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	r.say("fake: ran %s\n%s", line, strings.TrimRight(out.String(), "\n"))
	if err != nil {
		return fmt.Errorf("%s: %w", line, err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	r.output = strings.TrimSpace(lines[len(lines)-1])
	return nil
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

// opImage uploads a small generated PNG named NAME to a ticket, as
// `omo ai image add` does, and comments TEXT followed by the image's Markdown
// reference.
func opImage(ctx context.Context, r *runner, s Step) error {
	n, err := strconv.Atoi(strings.TrimSpace(s.Args[0]))
	if err != nil {
		return fmt.Errorf("%q is not a ticket number", s.Args[0])
	}
	c, err := r.api()
	if err != nil {
		return err
	}
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for x := range 32 {
		for y := range 24 {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: 96, B: uint8(y * 10), A: 255})
		}
	}
	if err := png.Encode(&b, img); err != nil {
		return err
	}
	im, err := c.UploadImage(ctx, n, s.Args[1], b.Bytes())
	if err != nil {
		return err
	}
	r.say("fake: image %s added to #%d", im.ID, n)
	return r.comment(ctx, n, strings.TrimSpace(arg(s, 2)+" "+im.Markdown), false)
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

// opCreateTicket creates a story or a standalone task as the agent (the
// CEO), in project PROJECT or, for a task, in none ("-").
func opCreateTicket(ctx context.Context, r *runner, s Step) error {
	c, err := r.api()
	if err != nil {
		return err
	}
	req := api.CreateTicket{Type: api.TicketType(s.Args[0]), Title: s.Args[2]}
	if p := strings.TrimSpace(s.Args[1]); p != "-" {
		req.Projects = []string{p}
	}
	t, err := c.CreateTicket(ctx, req)
	if err != nil {
		return err
	}
	r.created = append(r.created, t.Number)
	r.say("fake: created #%d %s", t.Number, t.Title)
	return nil
}

// opReleaseCreated moves every ticket create-ticket made to ready, as `omo
// ai ticket move N ready` does, and prints each refusal instead of failing.
func opReleaseCreated(ctx context.Context, r *runner, _ Step) error {
	c, err := r.api()
	if err != nil {
		return err
	}
	for _, n := range r.created {
		if _, err := c.Move(ctx, n, api.MoveRequest{Column: api.ColumnReady}); err != nil {
			r.say("fake: #%d not released: %v", n, err)
			continue
		}
		r.say("fake: released #%d", n)
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
	me, err := r.whoami(ctx)
	if err != nil {
		return err
	}
	role := me.Actor.Role
	for {
		if err := r.wait(ctx, s.Args[0]); err != nil {
			return err
		}
		t, err := r.ownTicket(ctx)
		if err != nil {
			return err
		}
		// The working agent works again when its ticket is back in
		// progress; a reviewer never does: the next round has its own.
		if t.Column == api.ColumnInProgress && role != api.RoleReviewer {
			r.say("fake: #%d is back in progress, working again", t.Number)
			if err := r.runRange(ctx, r.readyIdx+1, s.Index); err != nil {
				return err
			}
			continue
		}
		// A kept agent answers a mention of its role, or a question to it,
		// with ANSWER on its ticket.
		if answer := arg(s, 1); answer != "" &&
			(strings.Contains(r.reason, "mentions @"+string(role)) || strings.Contains(r.reason, "question")) {
			if err := r.comment(ctx, t.Number, answer, false); err != nil {
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

// checkJSONObject checks that field i, when given, is a JSON object.
func checkJSONObject(i int) func([]string) error {
	return func(args []string) error {
		if i >= len(args) || args[i] == "" || strings.Contains(args[i], "$") {
			return nil // placeholders are replaced when the op runs
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(args[i]), &v); err != nil {
			return fmt.Errorf("%q is not a JSON object", args[i])
		}
		return nil
	}
}

// opMCP calls TOOL with the arguments JSON (an object, {} when left out)
// through omo's MCP bridge, the way an agent program does: it starts the
// bridge as a stdio server with its own environment, so the bridge acts as
// this agent's session. It prints the tool's text; a tool error fails the
// op.
func opMCP(ctx context.Context, r *runner, s Step) error {
	args := map[string]any{}
	if raw := strings.TrimSpace(arg(s, 1)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return fmt.Errorf("mcp %s: arguments %q are not a JSON object", s.Args[0], raw)
		}
	}
	cs, err := connectBridge(ctx)
	if err != nil {
		return err
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: s.Args[0], Arguments: args})
	if err != nil {
		return fmt.Errorf("mcp %s: %w", s.Args[0], err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return fmt.Errorf("mcp %s: %s", s.Args[0], text.String())
	}
	r.say("fake: mcp %s: %s", s.Args[0], text.String())
	return nil
}

// opMCPTools prints the names of the tools omo's MCP bridge offers this
// agent's session, sorted.
func opMCPTools(ctx context.Context, r *runner, _ Step) error {
	cs, err := connectBridge(ctx)
	if err != nil {
		return err
	}
	defer cs.Close()
	var names []string
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("mcp tools: %w", err)
		}
		names = append(names, t.Name)
	}
	slices.Sort(names)
	r.say("fake: mcp tools: %s", strings.Join(names, ", "))
	return nil
}

// connectBridge starts omo's MCP bridge as this process with the agent's
// environment and connects to it as an agent program does.
func connectBridge(ctx context.Context) (*mcp.ClientSession, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), EnvEnable+"=1", EnvBridge+"=1")
	c := mcp.NewClient(&mcp.Implementation{Name: "fake-agent", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &mcp.CommandTransport{Command: cmd}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		return nil, fmt.Errorf("mcp: start the bridge: %w", err)
	}
	return cs, nil
}
