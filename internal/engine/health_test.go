package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// setHealth sets the health thresholds.
func (h *harness) setHealth(silent, question time.Duration) {
	h.t.Helper()
	g := h.st.General()
	g.Health.SilentAfter = settings.Duration(silent)
	g.Health.QuestionUnansweredAfter = settings.Duration(question)
	if err := h.st.SetGeneral(g); err != nil {
		h.t.Fatal(err)
	}
}

// check advances the clock by d and runs the health checks.
func (h *harness) check(d time.Duration) {
	h.t.Helper()
	h.clock.Advance(d)
	h.e.checkHealth(h.ctx, h.clock.Now())
}

// setOutput makes a fake agent show new output.
func (a *fakeAgent) setOutput(t time.Time) {
	a.mu.Lock()
	a.last = t
	a.mu.Unlock()
}

// stalledLines counts the stalled reminders typed into an agent.
func stalledLines(a *fakeAgent) int {
	n := 0
	for _, l := range a.typed() {
		if strings.HasPrefix(l, "you have produced no output for") {
			n++
		}
	}
	return n
}

func TestSilentAgentGetsOneReminderThenAttention(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHealth(15*time.Minute, 24*time.Hour)
	task, dev := h.readyDeveloper("one")
	a := h.agent(dev.ID)

	h.check(14 * time.Minute)
	if n := stalledLines(a); n != 0 {
		t.Fatalf("%d reminders after 14m of silence", n)
	}
	h.check(2 * time.Minute)
	if got := a.typed(); len(got) != 1 || got[0] != "you have produced no output for 16m. "+
		"If you are still working, carry on. If you have handed in your work or given your verdict, or are waiting for someone, run omo ai wait --timeout 30m." {
		t.Fatalf("typed %q", got)
	}
	h.check(0)
	h.check(14 * time.Minute)
	if n := stalledLines(a); n != 1 {
		t.Fatalf("%d reminders", n)
	}
	if tk := h.ticket(task); tk.FailedStep != api.FailedNone {
		t.Fatalf("flagged %s 14m after the reminder", tk.FailedStep)
	}
	h.check(2 * time.Minute)
	tk := h.ticket(task)
	if tk.FailedStep != api.FailedAgent || !strings.HasPrefix(tk.Attention, dev.Name+" has produced no output for 32m") {
		t.Fatalf("ticket %s %q", tk.FailedStep, tk.Attention)
	}
	last := h.lastComment(task).Body
	for _, want := range []string{"Needs attention:", "16m since omo reminded it", "retry the ticket", "cancel the ticket"} {
		if !strings.Contains(last, want) {
			t.Fatalf("comment lacks %q: %q", want, last)
		}
	}
	h.check(time.Hour)
	h.check(time.Hour)
	if n := stalledLines(a); n != 1 {
		t.Fatalf("%d reminders after the flag", n)
	}
	if n := h.commentsWith(task, "has produced no output"); n != 1 {
		t.Fatalf("%d flag comments", n)
	}
	if a.isKilled() {
		t.Fatal("the flag killed the agent; the supervisor decides")
	}
}

func TestAttentionClearedWhenAgentResumes(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHealth(15*time.Minute, 24*time.Hour)
	task, dev := h.readyDeveloper("one")
	a := h.agent(dev.ID)
	h.check(16 * time.Minute)
	h.check(16 * time.Minute)
	if h.ticket(task).FailedStep != api.FailedAgent {
		t.Fatal("not flagged")
	}
	a.setOutput(time.Now().Add(time.Hour))
	h.check(30 * time.Second)
	if tk := h.ticket(task); tk.FailedStep != api.FailedNone || tk.Attention != "" {
		t.Fatalf("still flagged: %s %q", tk.FailedStep, tk.Attention)
	}
	// A new silence is a new episode with a reminder of its own.
	h.check(14 * time.Minute)
	if n := stalledLines(a); n != 1 {
		t.Fatalf("%d reminders 14m into the new silence", n)
	}
	h.check(2 * time.Minute)
	if n := stalledLines(a); n != 2 {
		t.Fatalf("%d reminders after the new silence", n)
	}
}

func TestOutputPostponesTheReminder(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHealth(15*time.Minute, 24*time.Hour)
	task, dev := h.readyDeveloper("one")
	a := h.agent(dev.ID)
	h.check(10 * time.Minute)
	a.setOutput(time.Now().Add(time.Hour))
	h.check(10 * time.Minute) // 20m since the start, 0 since the output was seen
	h.check(14 * time.Minute)
	if n := stalledLines(a); n != 0 {
		t.Fatalf("%d reminders 14m after the last output", n)
	}
	h.check(2 * time.Minute)
	if n := stalledLines(a); n != 1 {
		t.Fatalf("%d reminders 16m after the last output", n)
	}
	// Output after the reminder answers it: no flag.
	a.setOutput(time.Now().Add(2 * time.Hour))
	h.check(time.Minute)
	h.check(15 * time.Minute)
	if tk := h.ticket(task); tk.FailedStep != api.FailedNone {
		t.Fatalf("flagged %s although the agent answered the reminder", tk.FailedStep)
	}
}

func TestReminderEchoIsNotAnAnswer(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHealth(15*time.Minute, 24*time.Hour)
	task, dev := h.readyDeveloper("one")
	a := h.agent(dev.ID)
	h.check(16 * time.Minute)
	// The terminal shows the typed reminder: a new transcript line within
	// moments of the reminder.
	a.setOutput(time.Now())
	h.check(30 * time.Second)
	h.check(16 * time.Minute)
	if tk := h.ticket(task); tk.FailedStep != api.FailedAgent {
		t.Fatalf("the echo of the reminder kept the agent from being flagged (%s)", tk.FailedStep)
	}
}

func TestWaitingAgentIsNeverStalled(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHealth(15*time.Minute, 24*time.Hour)
	task, dev := h.readyDeveloper("one")
	a := h.agent(dev.ID)
	ctx, cancel := context.WithCancel(h.ctx)
	ch := h.park(ctx, dev.ID, 24*time.Hour)
	for range 20 {
		h.check(time.Hour)
	}
	if n := len(a.typed()); n != 0 {
		t.Fatalf("%d lines typed into a waiting agent", n)
	}
	if tk := h.ticket(task); tk.FailedStep != api.FailedNone {
		t.Fatalf("a waiting agent was flagged: %s", tk.FailedStep)
	}
	cancel()
	h.result(ch)
	// Back at work, the silence counts from the wait's end.
	h.check(14 * time.Minute)
	if n := stalledLines(a); n != 0 {
		t.Fatalf("%d reminders 14m after the wait", n)
	}
	h.check(2 * time.Minute)
	if n := stalledLines(a); n != 1 {
		t.Fatalf("%d reminders 16m after the wait", n)
	}
}

func TestStartingAgentAndCEOAreNeverStalled(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHealth(15*time.Minute, 24*time.Hour)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task) // never runs omo ai ready
	ceo, err := h.e.StartCEO(h.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Ready(h.ctx, h.actor(ceo.ID)); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		h.check(time.Hour)
	}
	for _, id := range []string{dev.ID, ceo.ID} {
		if n := len(h.agent(id).typed()); n != 0 {
			t.Fatalf("%d lines typed", n)
		}
	}
	if tk := h.ticket(task); tk.FailedStep != api.FailedNone {
		t.Fatalf("a starting agent was flagged: %s", tk.FailedStep)
	}
}

func TestTicketMoveEndsTheSilence(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHealth(15*time.Minute, 24*time.Hour)
	task, dev := h.readyDeveloper("one")
	a := h.agent(dev.ID)
	h.check(16 * time.Minute)
	if _, err := h.b.Done(h.ctx, h.actor(dev.ID), api.DoneRequest{Comment: "finished"}); err != nil {
		t.Fatal(err)
	}
	h.check(16 * time.Minute)
	if tk := h.ticket(task); tk.FailedStep != api.FailedNone {
		t.Fatalf("flagged %s after the hand-in", tk.FailedStep)
	}
	if n := stalledLines(a); n != 1 {
		t.Fatalf("%d reminders", n)
	}
}

func TestStallFlagKeepsAnotherFailedStep(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHealth(15*time.Minute, 24*time.Hour)
	task, _ := h.readyDeveloper("one")
	h.check(16 * time.Minute)
	if err := h.b.SetFailed(h.ctx, task, api.FailedMerge, "the merge is blocked"); err != nil {
		t.Fatal(err)
	}
	h.check(16 * time.Minute)
	h.check(time.Hour)
	if tk := h.ticket(task); tk.FailedStep != api.FailedMerge || tk.Attention != "the merge is blocked" {
		t.Fatalf("ticket %s %q", tk.FailedStep, tk.Attention)
	}
}

// questionWorld starts a story's product manager and a task developer, both
// acknowledged, with the clock at the board's own time. It returns the
// task, the pm and the developer.
func (h *harness) questionWorld() (int64, board.EngineSession, board.EngineSession) {
	h.t.Helper()
	h.typeAtOnce()
	h.setHealth(24*time.Hour, 30*time.Minute)
	s := h.story("story")
	h.approve(s)
	h.step()
	pm := h.live(api.RolePM, s)
	if _, err := h.e.Ready(h.ctx, h.actor(pm.ID)); err != nil {
		h.t.Fatal(err)
	}
	task := h.task(s, "task")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	if _, err := h.e.Ready(h.ctx, h.actor(dev.ID)); err != nil {
		h.t.Fatal(err)
	}
	return task, pm, dev
}

// useBoardClock sets the engine's clock to the board's time, which stamps
// comments.
func (h *harness) useBoardClock() {
	h.clock.mu.Lock()
	h.clock.t = time.Now()
	h.clock.mu.Unlock()
}

func TestUnansweredQuestionRemindsThenFlags(t *testing.T) {
	h := newHarness(t)
	h.useBoardClock()
	task, pm, dev := h.questionWorld()
	q := h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "which way?", Question: true})
	pa := h.agent(pm.ID)
	h.check(29 * time.Minute)
	if n := len(pa.typed()); n != 0 {
		t.Fatalf("%d lines to the pm before the threshold", n)
	}
	h.check(2 * time.Minute)
	want := fmt.Sprintf("question %d on #%d still waits for your answer: omo ai comments %d --questions", q, task, task)
	if got := pa.typed(); len(got) != 1 || got[0] != want {
		t.Fatalf("pm got %q, want %q", got, want)
	}
	if n := len(h.agent(dev.ID).typed()); n != 0 {
		t.Fatalf("the asker got %d lines", n)
	}
	h.check(28 * time.Minute)
	if tk := h.ticket(task); tk.Attention != "" {
		t.Fatalf("flagged before twice the threshold: %q", tk.Attention)
	}
	h.check(2 * time.Minute)
	tk := h.ticket(task)
	if !strings.HasPrefix(tk.Attention, fmt.Sprintf("question %d to the product manager has waited 1h", q)) {
		t.Fatalf("attention %q", tk.Attention)
	}
	h.check(time.Hour)
	if n := len(pa.typed()); n != 1 {
		t.Fatalf("pm reminded %d times", n)
	}
	if n := h.commentsWith(task, fmt.Sprintf("question %d to the", q)); n != 1 {
		t.Fatalf("%d flag comments", n)
	}
	h.comment(h.actor(pm.ID), task, api.CommentRequest{Body: "this way", ReplyTo: &q})
	h.check(time.Minute)
	if tk := h.ticket(task); tk.Attention != "" {
		t.Fatalf("attention %q after the answer", tk.Attention)
	}
	h.check(time.Hour)
	if n := len(pa.typed()); n != 1 {
		t.Fatalf("pm reminded %d times after the answer", n)
	}
}

func TestQuestionToUserFlagsAtOnce(t *testing.T) {
	h := newHarness(t)
	h.useBoardClock()
	h.typeAtOnce()
	h.setHealth(24*time.Hour, 30*time.Minute)
	task, dev := h.readyDeveloper("one")
	q := h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "which way?", Question: true})
	h.check(29 * time.Minute)
	if tk := h.ticket(task); tk.Attention != "" {
		t.Fatalf("flagged before the threshold: %q", tk.Attention)
	}
	h.check(2 * time.Minute)
	if tk := h.ticket(task); !strings.HasPrefix(tk.Attention, fmt.Sprintf("question %d to the user has waited 3", q)) {
		t.Fatalf("attention %q", tk.Attention)
	}
	if n := len(h.agent(dev.ID).typed()); n != 0 {
		t.Fatalf("%d lines typed for a question to the user", n)
	}
	h.comment(board.User, task, api.CommentRequest{Body: "this way", ReplyTo: &q})
	h.check(time.Minute)
	if tk := h.ticket(task); tk.Attention != "" {
		t.Fatalf("attention %q after the answer", tk.Attention)
	}
}

func TestQuestionFlagKeepsOtherAttention(t *testing.T) {
	h := newHarness(t)
	h.useBoardClock()
	h.typeAtOnce()
	h.setHealth(24*time.Hour, 30*time.Minute)
	task, dev := h.readyDeveloper("one")
	if err := h.b.SetAttention(h.ctx, task, "look at the logs"); err != nil {
		t.Fatal(err)
	}
	q := h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "which way?", Question: true})
	h.check(time.Hour)
	if tk := h.ticket(task); tk.Attention != "look at the logs" {
		t.Fatalf("attention %q", tk.Attention)
	}
	h.comment(board.User, task, api.CommentRequest{Body: "this way", ReplyTo: &q})
	h.check(time.Minute)
	if tk := h.ticket(task); tk.Attention != "look at the logs" {
		t.Fatalf("the answer cleared somebody else's attention: %q", tk.Attention)
	}
}

// ---- git environment ----

// gitEnvRecorder replaces the steps of syncGitEnvironment: expansions of an
// environment whose author is in slow block until release is closed, and
// every environment handed to git is recorded.
type gitEnvRecorder struct {
	mu      sync.Mutex
	applied [][]string
	expands int
	fail    error
}

func (r *gitEnvRecorder) last() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.applied) == 0 {
		return nil
	}
	return r.applied[len(r.applied)-1]
}

func (h *harness) recordGitEnv(slow string, entered chan<- struct{}, release <-chan struct{}) *gitEnvRecorder {
	r := &gitEnvRecorder{}
	h.e.expandEnv = func(env map[string]string) ([]string, error) {
		r.mu.Lock()
		r.expands++
		fail := r.fail
		r.mu.Unlock()
		if env["GIT_AUTHOR_NAME"] == slow {
			entered <- struct{}{}
			<-release
		}
		if fail != nil {
			return nil, fail
		}
		return []string{"GIT_AUTHOR_NAME=" + env["GIT_AUTHOR_NAME"]}, nil
	}
	h.e.setGitEnv = func(env []string) {
		r.mu.Lock()
		r.applied = append(r.applied, env)
		r.mu.Unlock()
	}
	return r
}

func (h *harness) setAuthor(name string) {
	h.t.Helper()
	g := h.st.General()
	g.AgentEnv = map[string]string{"GIT_AUTHOR_NAME": name}
	if err := h.st.SetGeneral(g); err != nil {
		h.t.Fatal(err)
	}
}

func hasVar(env []string, kv string) bool {
	for _, x := range env {
		if x == kv {
			return true
		}
	}
	return false
}

func TestSlowOlderGitEnvironmentNeverOverwritesNewer(t *testing.T) {
	h := newHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	r := h.recordGitEnv("Old", entered, release)
	h.setAuthor("Old")
	first := make(chan struct{})
	go func() {
		h.e.checkHealth(h.ctx, h.clock.Now()) // the health tick
		close(first)
	}()
	<-entered
	// The settings change while the old environment is being expanded.
	h.setAuthor("New")
	second := make(chan struct{})
	go func() {
		h.e.SettingsChanged() // the settings worker
		close(second)
	}()
	select {
	case <-second:
		t.Fatal("a second expansion ran while the first was still under way")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	h.awaitClosed("the health check", first)
	h.awaitClosed("the settings change", second)
	h.e.recoverAtStart(h.ctx) // the start: nothing new to apply
	if got := r.last(); !hasVar(got, "GIT_AUTHOR_NAME=New") {
		t.Fatalf("git got %q last, want the newer environment", got)
	}
}

func TestFailedGitEnvironmentIsRetriedAndLoggedOnce(t *testing.T) {
	h := newHarness(t)
	r := h.recordGitEnv("", nil, nil)
	h.e.syncGitEnvironment()
	if !hasVar(r.last(), "GIT_AUTHOR_NAME=Agent") {
		t.Fatalf("git got %q", r.last())
	}
	r.mu.Lock()
	r.fail = errors.New("command `pass show git` timed out")
	r.mu.Unlock()
	h.setAuthor("Broken")
	for range 3 {
		h.e.checkHealth(h.ctx, h.clock.Now())
	}
	if n := strings.Count(h.logs.String(), "agent environment for git"); n != 1 {
		t.Fatalf("the failure was logged %d times, want once:\n%s", n, h.logs.String())
	}
	r.mu.Lock()
	applied, expands := len(r.applied), r.expands
	r.fail = nil
	r.mu.Unlock()
	if applied != 1 || !hasVar(r.last(), "GIT_AUTHOR_NAME=Agent") {
		t.Fatalf("git got %d environments, last %q; a failed expansion must keep the previous one", applied, r.last())
	}
	if expands != 4 {
		t.Fatalf("%d expansions, want every health check to try again", expands)
	}
	// The cause went away: the next health check applies it.
	h.e.checkHealth(h.ctx, h.clock.Now())
	if !hasVar(r.last(), "GIT_AUTHOR_NAME=Broken") {
		t.Fatalf("git got %q after the cause went away", r.last())
	}
}
