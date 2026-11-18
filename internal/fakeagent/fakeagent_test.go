package fakeagent

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"github.com/scolastico-dev/one-man-office/internal/gittest"
)

func TestMain(m *testing.M) {
	if Enabled() {
		os.Exit(Main())
	}
	gittest.Isolate()
	os.Exit(childenvtest.Run(m))
}

func contains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("missing %q in:\n%s", want, got)
	}
}

func noSecret(t *testing.T, out, id string) {
	t.Helper()
	if id != "" && strings.Contains(out, id) {
		t.Fatalf("output contains the session id:\n%s", out)
	}
}

func TestDeveloperDefaultScriptCallsInOrder(t *testing.T) {
	w := startWorld(t)
	story := w.story("story")
	task := w.task("work", story)
	id := w.session(api.RoleDeveloper, task, "dev1")
	dir := t.TempDir()
	git(t, dir, "init", "-q")

	r := start(t, Options{Dir: dir})
	w.waitFor("POST /api/ai/wait")
	res := r.finish(true)

	var got []string
	for _, c := range w.calls() {
		if strings.HasPrefix(c, "POST /api/ai/") {
			got = append(got, c)
		}
	}
	want := []string{"POST /api/ai/ready", "POST /api/ai/done", "POST /api/ai/wait"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v, want %v", got, want)
	}
	if subject := git(t, dir, "log", "-1", "--format=%s"); subject != "feat: fake work for #"+itoa(task) {
		t.Fatalf("commit subject %q", subject)
	}
	if st := git(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("worktree not clean: %s", st)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "fake-"+itoa(task)+".txt"))
	if err != nil || string(raw) != "done by dev1\n" {
		t.Fatalf("file %q, %v", raw, err)
	}
	if tk := w.ticket(task); tk.Column != api.ColumnReview {
		t.Fatalf("ticket is in %s", tk.Column)
	}
	contains(t, res.out, "stand-in prompt for session [session id]")
	contains(t, res.out, "handed in #"+itoa(task))
	noSecret(t, res.out, id)
}

func TestScriptFromEnvironmentOverridesDefault(t *testing.T) {
	w := startWorld(t)
	task := w.task("work", w.story("story"))
	id := w.session(api.RoleDeveloper, task, "dev1")

	t.Setenv(EnvScriptRolePrefix+"DEVELOPER", "print|role script")
	before := len(w.calls())
	res := runFake(t, Options{})
	contains(t, res.out, "role script")
	if res.code != 0 || len(w.calls()) != before {
		t.Fatalf("code %d, calls %v", res.code, w.calls())
	}

	t.Setenv(EnvScript, "print|whole script")
	res = runFake(t, Options{})
	contains(t, res.out, "whole script")
	if strings.Contains(res.out, "role script") {
		t.Fatalf("role script ran:\n%s", res.out)
	}

	res = runFake(t, Options{Script: "print|option script"})
	contains(t, res.out, "option script")
	noSecret(t, res.out, id)
}

func TestExportJobUsesExportScript(t *testing.T) {
	w := startWorld(t)
	// A standalone task that is exported once approved: handing it in puts
	// it in Review at stage exporting.
	tk, err := w.user.CreateTicket(context.Background(), api.CreateTicket{
		Type: api.TypeTask, Title: "export me", Projects: []string{w.project},
		Review: api.ReviewSkipped, WhenApproved: api.WhenApprovedExport})
	if err != nil {
		t.Fatal(err)
	}
	w.begin(tk.Number)
	w.session(api.RoleDeveloper, tk.Number, "dev1")
	t.Setenv(EnvScriptRolePrefix+"EXPORT", "print|export script")
	t.Setenv(EnvScriptRolePrefix+"DEVELOPER", "print|developer script\ndone|work")

	res := runFake(t, Options{})
	contains(t, res.out, "developer script")
	if got := w.ticket(tk.Number); got.ReviewStage != api.ReviewStageExporting {
		t.Fatalf("stage %q", got.ReviewStage)
	}
	res = runFake(t, Options{})
	contains(t, res.out, "export script")
	if strings.Contains(res.out, "developer script") {
		t.Fatalf("developer script ran for an export job:\n%s", res.out)
	}
}

func TestPlaceholdersExpand(t *testing.T) {
	w := startWorld(t)
	story := w.story("story")
	task := w.task("work", story)
	w.session(api.RoleDeveloper, task, "dev1")
	res := runFake(t, Options{Script: "print|$TICKET/$STORY/$PROJECT/$NAME"})
	contains(t, res.out, itoa(task)+"/"+itoa(story)+"/"+w.project+"/dev1")
}

func TestCreateTasksChainLinksTasks(t *testing.T) {
	w := startWorld(t)
	story := w.story("story")
	w.session(api.RolePM, story, "pm1")
	r := start(t, Options{Script: "create-tasks|3|chain\nrelease-all\nuntil-tasks-done|1m"})
	w.waitFor("POST /api/ai/wait")
	calls := strings.Join(w.calls(), ",")
	contains(t, calls, "POST /api/tickets,POST /api/tickets,POST /api/tickets/3/dependencies,POST /api/tickets,POST /api/tickets/4/dependencies")

	if tk := w.ticket(3); len(tk.DependsOn) != 1 || tk.DependsOn[0] != 2 || tk.Story == nil || *tk.Story != story {
		t.Fatalf("task 3: %+v", tk)
	}
	if tk := w.ticket(4); len(tk.DependsOn) != 1 || tk.DependsOn[0] != 3 || tk.Projects[0].Project != w.project {
		t.Fatalf("task 4: %+v", tk)
	}
	for n := 2; n <= 4; n++ {
		if tk := w.ticket(n); tk.Column != api.ColumnReady || tk.Title != "task "+itoa(n-1) {
			t.Fatalf("task %d not released: %s %q", n, tk.Column, tk.Title)
		}
	}
	// Finish the tasks the only way the test can; the fake sees them done.
	for n := 2; n <= 4; n++ {
		if _, err := w.user.Cancel(context.Background(), n, api.CancelRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	w.waits <- "omo: all tasks are done"
	res := r.finish(false)
	if res.code != 0 {
		t.Fatalf("code %d:\n%s", res.code, res.out)
	}
	contains(t, res.out, "every task of the story is done or cancelled")
}

func TestHandoffOnStopCommentsAndWaitsAgain(t *testing.T) {
	w := startWorld(t)
	task := w.task("work", w.story("story"))
	w.session(api.RoleDeveloper, task, "dev1")
	w.waits <- "omo: omo is stopping; write a handoff comment"
	w.waits <- "omo: nothing else"
	res := runFake(t, Options{Script: "wait|1m\nhandoff-on-stop\nexit|0"})
	if res.code != 0 {
		t.Fatalf("code %d:\n%s", res.code, res.out)
	}
	comments := agentComments(t, w, task)
	if len(comments) != 1 || comments[0].Body != "Handoff: fake state" {
		t.Fatalf("comments %+v", comments)
	}
	contains(t, res.out, "woke up: omo: nothing else")
}

func TestHandoffOnStopIgnoresOtherReasons(t *testing.T) {
	w := startWorld(t)
	task := w.task("work", w.story("story"))
	w.session(api.RoleDeveloper, task, "dev1")
	w.waits <- "timeout: nothing arrived in 1m."
	res := runFake(t, Options{Script: "wait|1m\nhandoff-on-stop"})
	if res.code != 0 {
		t.Fatalf("code %d:\n%s", res.code, res.out)
	}
	if comments := agentComments(t, w, task); len(comments) != 0 {
		t.Fatalf("comments %+v", comments)
	}
}

func TestWaitLoopWorksAgainWhenRejected(t *testing.T) {
	w := startWorld(t)
	task := w.task("work", w.story("story"))
	w.session(api.RoleDeveloper, task, "dev1")
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	script := "ready\ndone|round\nwait-loop|1m"
	r := start(t, Options{Dir: dir, Script: script})
	w.waitFor("POST /api/ai/wait")
	if _, err := w.reject(task); err != nil {
		t.Fatal(err)
	}
	w.waits <- "omo: ticket was rejected"
	// The second hand-in is the sign the loop worked again.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if tk := w.ticket(task); tk.Column == api.ColumnReview && tk.ReviewRound == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not handed in again:\n%s", r.out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	res := r.finish(true)
	contains(t, res.out, "back in progress, working again")
}

// A kept agent answers what wakes it: with an answer text, wait-loop posts
// it on its ticket after a wake by a mention of its role. A reviewer never
// works again in the loop, also when its ticket is back in progress: the
// next round has its own reviewer.
func TestWaitLoopAnswersMentionsAndReviewerNeverJudgesAgain(t *testing.T) {
	w := startWorld(t)
	task := w.task("work", w.story("story"))
	w.session(api.RoleReviewer, task, "rev1")
	r := start(t, Options{Script: "wait-loop|1m|Yes, all tests ran"})
	w.waits <- "omo: #" + itoa(task) + " comment 5 by user mentions @reviewer: omo ai comments " + itoa(task) + " --since 4"
	deadline := time.Now().Add(20 * time.Second)
	for len(agentComments(t, w, task)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no answer:\n%s", r.out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.waits <- "timeout: nothing arrived in 1m."
	w.waitFor("POST /api/ai/wait")
	res := r.finish(true)
	if c := agentComments(t, w, task); len(c) != 1 || c[0].Body != "Yes, all tests ran" {
		t.Fatalf("comments %+v", c)
	}
	if strings.Contains(res.out, "working again") {
		t.Fatalf("the reviewer worked again:\n%s", res.out)
	}
}

func TestUnknownOpFailsWithLineNumber(t *testing.T) {
	w := startWorld(t)
	w.session(api.RoleDeveloper, w.task("work", w.story("story")), "dev1")
	before := len(w.calls())
	res := runFake(t, Options{Script: "# comment\nprint|a\nfrobnicate|x\nprint|b"})
	if res.code != ExitBadScript {
		t.Fatalf("code %d", res.code)
	}
	contains(t, res.out, `script line 3: unknown op "frobnicate"`)
	if strings.Contains(res.out, "\na\n") || len(w.calls()) != before {
		t.Fatalf("something ran:\n%s", res.out)
	}
}

func TestBadFieldsFailBeforeAnythingRuns(t *testing.T) {
	for script, want := range map[string]string{
		"print|a\nwait|soon":         "script line 2: op wait: \"soon\" is not a positive duration",
		"wait":                       "script line 1: op wait takes DURATION",
		"verdict|maybe":              "script line 1: op verdict: \"maybe\" is not one of approve, reject",
		"verdicts|reject,maybe":      "script line 1: op verdicts: \"maybe\" is not one of approve, reject",
		"exit|x":                     "script line 1: op exit: \"x\" is not a number",
		"create-tasks|0":             "script line 1: op create-tasks:",
		"create-tasks|2|loop":        "only chain is known",
		"create-ticket|epic|-|x":     "script line 1: op create-ticket: \"epic\" is not one of story, task",
		"release-created|now":        "script line 1: op release-created takes no fields",
		"ready|extra":                "script line 1: op ready takes no fields",
		"await-prompt|sideways":      "is not one of any, arg, typed",
		"print|first\n\n  unknown  ": "script line 3: unknown op \"unknown\"",
	} {
		res := runFake(t, Options{Script: script})
		if res.code != ExitBadScript {
			t.Fatalf("%q: code %d\n%s", script, res.code, res.out)
		}
		contains(t, res.out, want)
		if strings.HasPrefix(script, "print|a") && strings.Contains(res.out, "\na\n") {
			t.Fatalf("a line ran before the bad one:\n%s", res.out)
		}
	}
}

func TestTextFieldsKeepPipes(t *testing.T) {
	res := runFake(t, Options{Script: "print|a|b|c"})
	contains(t, res.out, "a|b|c")
}

func TestExitAndCrashCodes(t *testing.T) {
	for script, code := range map[string]int{"exit|0": 0, "exit|3": 3, "crash|7": 7, "print|x\nexit|2\nprint|never": 2} {
		res := runFake(t, Options{Script: script})
		if res.code != code {
			t.Fatalf("%q: code %d\n%s", script, res.code, res.out)
		}
		if strings.Contains(res.out, "never") {
			t.Fatalf("ran past exit:\n%s", res.out)
		}
	}
}

func TestFailingOpExitsWithError(t *testing.T) {
	// A commit in a folder that is not a repository fails.
	res := runFake(t, Options{Dir: t.TempDir(), Script: "write|a.txt|x\ncommit|nope\nprint|never"})
	if res.code != ExitFailed {
		t.Fatalf("code %d", res.code)
	}
	contains(t, res.out, "line 2 (commit) failed")
	if strings.Contains(res.out, "never") {
		t.Fatalf("ran on:\n%s", res.out)
	}
}

// TestCommitWithoutChangesAndGit commits twice without a change in between,
// as a script that works again after a rejection does, and runs git.
func TestCommitWithoutChangesAndGit(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	res := runFake(t, Options{Dir: dir, Script: "write|a.txt|x\ncommit|first\nwrite|a.txt|x\ncommit|again\ngit|switch|-q|-c|side"})
	if res.code != 0 {
		t.Fatalf("code %d\n%s", res.code, res.out)
	}
	if got := git(t, dir, "log", "--format=%s"); got != "again\nfirst" {
		t.Fatalf("log %q", got)
	}
	if got := git(t, dir, "branch", "--show-current"); got != "side" {
		t.Fatalf("branch %q", got)
	}
}

func TestWriteStaysInsideWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"../out.txt", "/tmp/out.txt", "a/../../out.txt"} {
		res := runFake(t, Options{Dir: dir, Script: "write|" + p + "|x"})
		if res.code != ExitFailed {
			t.Fatalf("%s accepted:\n%s", p, res.out)
		}
	}
	res := runFake(t, Options{Dir: dir, Script: "write|sub/dir/file.txt|hello"})
	if raw, err := os.ReadFile(filepath.Join(dir, "sub", "dir", "file.txt")); err != nil || string(raw) != "hello\n" {
		t.Fatalf("%q %v\n%s", raw, err, res.out)
	}
}

func TestLeaveUncommittedFilesAndHandInTwice(t *testing.T) {
	w := startWorld(t)
	task := w.task("work", w.story("story"))
	id := w.session(api.RoleDeveloper, task, "dev1")
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	res := runFake(t, Options{Dir: dir, Script: "write|dirty.txt|left behind\ndone|first\ndone|second"})
	if st := git(t, dir, "status", "--porcelain"); !strings.Contains(st, "dirty.txt") {
		t.Fatalf("status %q", st)
	}
	// The second hand-in is refused by the server and fails the script.
	if res.code != ExitFailed {
		t.Fatalf("code %d\n%s", res.code, res.out)
	}
	contains(t, res.out, "handed in #"+itoa(task))
	contains(t, res.out, "line 3 (done) failed")
	noSecret(t, res.out, id)
}

// hangContext observes the blocking context receive in hang. The print/hang
// script does not otherwise call Done, so this marks entry into the wait.
type hangContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *hangContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestNeverReadyAndHangMakeNoCalls(t *testing.T) {
	w := startWorld(t)
	w.session(api.RoleDeveloper, w.task("work", w.story("story")), "dev1")
	ctx, cancel := context.WithCancel(context.Background())
	hang := &hangContext{Context: ctx, entered: make(chan struct{})}
	r := &running{t: t, out: &syncBuffer{}, cancel: cancel, done: make(chan struct{})}
	go func() {
		r.code = Run(hang, Options{Script: "print|alive\nhang", Stdout: r.out})
		close(r.done)
	}()
	t.Cleanup(func() { r.finish(true) })
	select {
	case <-hang.entered:
	case <-r.done:
		t.Fatalf("hang returned %d", r.code)
	case <-time.After(20 * time.Second):
		t.Fatal("hang did not reach its context wait")
	}
	contains(t, r.out.String(), "alive")
	res := r.finish(true)
	if res.code == 0 {
		t.Fatal("interrupted agent reported success")
	}
	for _, c := range w.calls() {
		if strings.Contains(c, "/api/ai/") {
			t.Fatalf("hanging agent called %s", c)
		}
	}
}

func TestFloodPrintsLines(t *testing.T) {
	res := runFake(t, Options{Script: "flood|500"})
	if n := strings.Count(res.out, "flood line"); n != 500 {
		t.Fatalf("%d lines", n)
	}
	r := start(t, Options{Script: "flood"})
	deadline := time.Now().Add(20 * time.Second)
	for strings.Count(r.out.String(), "\n") < 1000 {
		if time.Now().After(deadline) {
			t.Fatal("endless flood stopped")
		}
		time.Sleep(time.Millisecond)
	}
	if res := r.finish(true); res.code == 0 {
		t.Fatal("interrupted flood reported success")
	}
}

func TestStartPromptDeliveryStyles(t *testing.T) {
	prompt := "Run omo ai ready to begin"
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"positional (codex)":            {[]string{"-test.run=^$", "-c", "projects=x", prompt}, "as an argument: " + prompt},
		"flag value (gemini)":           {[]string{"--prompt-interactive", prompt}, "as the value of --prompt-interactive: " + prompt},
		"flag with equals":              {[]string{"--prompt-interactive=" + prompt}, "as the value of --prompt-interactive: " + prompt},
		"after trust settings (agy)":    {[]string{"--model", "m", "--dangerously-skip-permissions", "--settings", `{"trustedWorkspaces":["/w"]}`, "--prompt-interactive", prompt}, "as the value of --prompt-interactive: " + prompt},
		"substituted into one (custom)": {[]string{"-x", "say: " + prompt}, "as the value of -x"},
		"test flags are not the prompt": {[]string{"-test.run=omo ai ready", prompt}, "as an argument: " + prompt},
	} {
		res := runFake(t, Options{Args: tc.args, Script: "await-prompt|arg\nexit|0"})
		if res.code != 0 {
			t.Fatalf("%s: code %d\n%s", name, res.code, res.out)
		}
		contains(t, res.out, tc.want)
	}
	res := runFake(t, Options{Script: "await-prompt|arg"})
	if res.code != ExitFailed {
		t.Fatalf("arg mode without argument: code %d", res.code)
	}
}

func TestTypedStartPromptAndNotifications(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := start(t, Options{Stdin: pr, Script: "await-prompt|typed\nawait-line|omo: new comment\nprint|got it\nexit|0"})
	io.WriteString(pw, "some noise\r\n")
	io.WriteString(pw, "Run omo ai ready to begin\r\n")
	io.WriteString(pw, "omo: other news\n")
	io.WriteString(pw, "omo: new comment on #4; run omo ai comments 4\n")
	res := r.finish(false)
	if res.code != 0 {
		t.Fatalf("code %d\n%s", res.code, res.out)
	}
	contains(t, res.out, "start prompt received by typing: Run omo ai ready to begin")
	contains(t, res.out, "typed: omo: other news")
	contains(t, res.out, "got it")
}

func TestPromptIsTypedBeforeReadyWithEveryStyleMixed(t *testing.T) {
	// "any" takes the command line first and does not read input.
	pr, pw := io.Pipe()
	defer pw.Close()
	res := runFake(t, Options{Stdin: pr, Args: []string{"Run omo ai ready"}, Script: "await-prompt\nexit|0"})
	contains(t, res.out, "as an argument")
}

func TestIgnoredNotificationsAreHarmless(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		for i := 0; i < 50; i++ {
			io.WriteString(pw, "omo: ping\n")
		}
		pw.Close()
	}()
	res := runFake(t, Options{Stdin: pr, Script: "print|busy\nsleep|20ms\nexit|0"})
	if res.code != 0 {
		t.Fatalf("code %d", res.code)
	}
}

func TestAwaitLineTimesOut(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	res := runFake(t, Options{Stdin: pr, Script: "await-line|never|50ms"})
	if res.code != ExitFailed {
		t.Fatalf("code %d", res.code)
	}
	contains(t, res.out, `no typed line containing "never"`)
}

func TestSavePrompt(t *testing.T) {
	w := startWorld(t)
	w.session(api.RoleDeveloper, w.task("work", w.story("story")), "dev1")
	dir := t.TempDir()
	res := runFake(t, Options{Dir: dir, Script: "ready\nsave-prompt|prompt.txt"})
	if res.code != 0 {
		t.Fatalf("code %d\n%s", res.code, res.out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "prompt.txt"))
	if err != nil || !strings.HasPrefix(string(raw), "stand-in prompt") {
		t.Fatalf("%q %v", raw, err)
	}
}

func TestCommentAskAndVerdictOps(t *testing.T) {
	w := startWorld(t)
	story := w.story("story")
	task := w.task("work", story)
	w.session(api.RoleDeveloper, task, "dev1")
	res := runFake(t, Options{Script: "comment|$TICKET|hello\nask|may I?\ncomment|" + itoa(task) + "|a note"})
	if res.code != 0 {
		t.Fatalf("code %d\n%s", res.code, res.out)
	}
	comments := agentComments(t, w, task)
	if len(comments) != 3 {
		t.Fatalf("%+v", comments)
	}
	if comments[1].QuestionTo == "" || comments[0].QuestionTo != "" {
		t.Fatalf("ask did not post a question: %+v", comments)
	}
}

// The mcp op calls omo's tools through the bridge, as the agent's session:
// the comment lands on its own ticket with the agent as author.
func TestMCPOpCallsAToolThroughTheBridge(t *testing.T) {
	w := startWorld(t)
	task := w.task("work", w.story("story"))
	id := w.session(api.RoleDeveloper, task, "dev1")
	res := runFake(t, Options{Script: "mcp|comment|{\"ticket\":$TICKET,\"body\":\"through the bridge\"}\nmcp|whoami"})
	if res.code != 0 {
		t.Fatalf("code %d\n%s", res.code, res.out)
	}
	contains(t, res.out, "fake: mcp whoami:")
	contains(t, res.out, "dev1")
	noSecret(t, res.out, id)
	comments := agentComments(t, w, task)
	if len(comments) != 1 || comments[0].Body != "through the bridge" || comments[0].Author.Name != "dev1" {
		t.Fatalf("%+v", comments)
	}
	// A tool error fails the op.
	res = runFake(t, Options{Script: "mcp|wait|{\"timeout\":\"1m\"}"})
	if res.code != ExitFailed || !strings.Contains(res.out, "run `omo ai wait` in the terminal") {
		t.Fatalf("code %d\n%s", res.code, res.out)
	}
}

// The mcp-tools op lists the tools omo offers the agent's session through
// the bridge, sorted.
func TestMCPToolsOpListsTheToolsOfTheSession(t *testing.T) {
	w := startWorld(t)
	task := w.task("work", w.story("story"))
	id := w.session(api.RoleDeveloper, task, "dev1")
	res := runFake(t, Options{Script: "mcp-tools"})
	if res.code != 0 {
		t.Fatalf("code %d\n%s", res.code, res.out)
	}
	contains(t, res.out, "fake: mcp tools: ")
	contains(t, res.out, "comment, comments, done, ")
	if strings.Contains(res.out, " wait") {
		t.Fatalf("a command line only tool is listed:\n%s", res.out)
	}
	noSecret(t, res.out, id)
}

func TestImageOpUploadsAndComments(t *testing.T) {
	w := startWorld(t)
	task := w.task("work", w.story("story"))
	w.session(api.RoleDeveloper, task, "dev1")
	res := runFake(t, Options{Script: "image|$TICKET|screen shot.png|Here it is:"})
	if res.code != 0 {
		t.Fatalf("code %d\n%s", res.code, res.out)
	}
	comments := agentComments(t, w, task)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Body, "Here it is: ![screen shot.png](omo-image:") {
		t.Fatalf("%+v", comments)
	}
}

func TestDoneURLAndFailedSendTheirFields(t *testing.T) {
	w := startWorld(t)
	task := w.task("work", w.story("story"))
	w.session(api.RoleDeveloper, task, "dev1")
	// The server decides whether the report fits the ticket; the ops only
	// have to send the fields.
	runFake(t, Options{Script: "done-url|https://example.invalid/pr/$TICKET"})
	runFake(t, Options{Script: "done-failed|push refused"})
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.doneBodies) != 2 {
		t.Fatalf("bodies %v", w.doneBodies)
	}
	contains(t, w.doneBodies[0], `"url":"https://example.invalid/pr/`+itoa(task)+`"`)
	contains(t, w.doneBodies[1], `"error":"push refused"`)
}

func TestDefaultsParse(t *testing.T) {
	for role, script := range defaults {
		if _, err := Parse(script); err != nil {
			t.Errorf("default script of %s: %v", role, err)
		}
	}
	for name := range ops {
		if Ops()[name] == "" && name != "ready" && name != "release-all" && name != "hang" {
			t.Errorf("op %s has no usage", name)
		}
	}
}

// TestRunsAsProcess starts the test binary as the fake agent, the way an
// engine test starts it from a profile: the command is the test binary, the
// environment comes from Env, HOME is empty, the start prompt is typed.
func TestRunsAsProcess(t *testing.T) {
	w := startWorld(t)
	story := w.story("story")
	task := w.task("work", story)
	id := w.session(api.RoleDeveloper, task, "dev1")
	exe, err := Command()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Dir = dir
	home := t.TempDir()
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "OMO_SOCKET=" + w.socket, "OMO_AGENT_ID=" + id}
	for k, v := range Env("await-prompt|typed\nready\nwrite|f.txt|x\ncommit|feat: x\ndone|ok\nexit|4") {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out syncBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	werr := make(chan error, 1)
	waited := make(chan struct{})
	go func() { defer close(waited); werr <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-waited
	})
	io.WriteString(in, "Run omo ai ready to begin\n")
	select {
	case <-werr:
	case <-time.After(30 * time.Second):
		t.Fatalf("process did not exit:\n%s", out.String())
	}
	if code := cmd.ProcessState.ExitCode(); code != 4 {
		t.Fatalf("exit code %d\n%s", code, out.String())
	}
	contains(t, out.String(), "start prompt received by typing")
	contains(t, out.String(), "stand-in prompt for session [session id]")
	noSecret(t, out.String(), id)
	if tk := w.ticket(task); tk.Column != api.ColumnReview {
		t.Fatalf("ticket in %s", tk.Column)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("the fake agent wrote into HOME: %v", entries)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func (w *world) reject(n int) (api.Ticket, error) {
	var out api.Ticket
	err := w.user.Do(context.Background(), http.MethodPost, "/api/tickets/"+itoa(n)+"/reject", api.RejectRequest{Comment: "again"}, &out)
	return out, err
}

// agentComments returns the comments agents wrote on ticket n, without the
// system's own.
func agentComments(t *testing.T, w *world, n int) []api.Comment {
	t.Helper()
	all, err := w.user.Comments(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	var out []api.Comment
	for _, c := range all {
		if c.Author.Role != api.RoleSystem {
			out = append(out, c)
		}
	}
	return out
}

func TestComposerSwallowsEntersAndDropsEarlyInput(t *testing.T) {
	var echo syncBuffer
	in, feed := io.Pipe()
	lines := composer(in, &echo, 1, time.Time{}, 0)
	go func() {
		feed.Write([]byte("\rhello"))
		feed.Write([]byte("\r"))
		feed.Write([]byte("x\x7f!\r"))
		feed.Close()
	}()
	got, _ := io.ReadAll(lines)
	if string(got) != "hello!\n" {
		t.Fatalf("lines %q", got)
	}
	if want := "hellox\b \b!\r\n"; echo.String() != want {
		t.Fatalf("echo %q, want %q", echo.String(), want)
	}

	in, feed = io.Pipe()
	lines = composer(in, io.Discard, 0, time.Now().Add(time.Hour), 0)
	go func() {
		feed.Write([]byte("lost\r"))
		feed.Close()
	}()
	if got, _ := io.ReadAll(lines); len(got) != 0 {
		t.Fatalf("input during the deaf time came through: %q", got)
	}
}

func TestComposerDropsWholePromptsWithoutEcho(t *testing.T) {
	var echo syncBuffer
	in, feed := io.Pipe()
	lines := composer(in, &echo, 0, time.Time{}, 1)
	go func() {
		defer feed.Close()
		_, _ = feed.Write([]byte("lost"))
		_, _ = feed.Write([]byte(" prompt\rkept prompt\r"))
	}()
	got, err := io.ReadAll(lines)
	if err != nil || string(got) != "kept prompt\n" || echo.String() != "kept prompt\r\n" {
		t.Fatalf("lines %q, echo %q, error %v", got, echo.String(), err)
	}
}

func TestComposerTakesAPasteAsOneLine(t *testing.T) {
	var echo syncBuffer
	in, feed := io.Pipe()
	lines := pasteComposer(in, &echo, 0, time.Time{}, 0, true)
	go func() {
		// The markers may be split across reads; the line breaks inside the
		// paste do not submit, the Enter after it does.
		feed.Write([]byte("\x1b[20"))
		feed.Write([]byte("0~alpha\rbeta\n\x1b[201"))
		feed.Write([]byte("~\r"))
		feed.Write([]byte("plain\r"))
		feed.Close()
	}()
	got, _ := io.ReadAll(lines)
	if want := "alpha⏎beta⏎\nplain\n"; string(got) != want {
		t.Fatalf("lines %q, want %q", got, want)
	}
	if !strings.HasPrefix(echo.String(), "\x1b[?2004h") {
		t.Fatalf("bracketed paste was not asked for: %q", echo.String())
	}
}

func TestComposerWithoutBracketedPasteSubmitsEveryLine(t *testing.T) {
	in, feed := io.Pipe()
	lines := composer(in, io.Discard, 0, time.Time{}, 0)
	go func() {
		feed.Write([]byte("alpha\rbeta\r"))
		feed.Close()
	}()
	if got, _ := io.ReadAll(lines); string(got) != "alpha\nbeta\n" {
		t.Fatalf("lines %q", got)
	}
}
