package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// notifyNow delivers every event not delivered yet.
func (h *harness) notifyNow() {
	h.e.notifyPass(h.ctx)
}

// typeAtOnce makes notifications for working agents be typed without the
// grace period.
func (h *harness) typeAtOnce() {
	h.e.nt.grace = 0
}

// readyDeveloper starts the developer of a new standalone task and lets it
// acknowledge its start. It returns the task and the session.
func (h *harness) readyDeveloper(title string) (int64, board.EngineSession) {
	h.t.Helper()
	task := h.standalone(title)
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	if _, err := h.e.Ready(h.ctx, h.actor(dev.ID)); err != nil {
		h.t.Fatal(err)
	}
	return task, dev
}

// comment posts a comment and returns its id.
func (h *harness) comment(a board.Actor, n int64, req api.CommentRequest) int {
	h.t.Helper()
	res, err := h.b.Comment(h.ctx, a, n, req)
	if err != nil {
		h.t.Fatal(err)
	}
	return res.Comment.ID
}

// sessionState reads a session's state.
func (h *harness) sessionState(id string) api.SessionState {
	h.t.Helper()
	all, err := h.b.AgentSessions(h.ctx, board.User, false)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, s := range all {
		if s.ID == id {
			return s.State
		}
	}
	return ""
}

type waitResult struct {
	reason string
	err    error
}

// park runs Wait for a session in the background and returns once the wait
// is parked (the session is waiting).
func (h *harness) park(ctx context.Context, id string, timeout time.Duration) <-chan waitResult {
	h.t.Helper()
	a := h.actor(id)
	out := make(chan waitResult, 1)
	go func() {
		r, err := h.e.Wait(ctx, a, timeout)
		out <- waitResult{r, err}
	}()
	h.waitFor("the wait to park", func() bool { return h.sessionState(id) == api.SessionWaiting })
	return out
}

// result waits for the outcome of a parked wait.
func (h *harness) result(ch <-chan waitResult) waitResult {
	h.t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(20 * time.Second):
		h.t.Fatal("the wait did not return")
		return waitResult{}
	}
}

// typed returns the lines typed into an agent.
func (a *fakeAgent) typed() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.lines...)
}

func TestMentionWakesWaitingDeveloper(t *testing.T) {
	h := newHarness(t)
	task, dev := h.readyDeveloper("one")
	ch := h.park(h.ctx, dev.ID, time.Hour)
	id := h.comment(board.User, task, api.CommentRequest{Body: "@developer please"})
	h.notifyNow()
	r := h.result(ch)
	want := fmt.Sprintf("omo: #%d comment %d by user mentions @developer: omo ai comments %d --since %d", task, id, task, id-1)
	if r.err != nil || r.reason != want {
		t.Fatalf("wait = %q, %v; want %q", r.reason, r.err, want)
	}
	if st := h.sessionState(dev.ID); st != api.SessionRunning {
		t.Fatalf("session state %s after the wait, want running", st)
	}
	if got := h.agent(dev.ID).typed(); len(got) != 0 {
		t.Fatalf("a released wait was typed as well: %q", got)
	}
}

func TestMentionTypesLineIntoRunningAgent(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	task, dev := h.readyDeveloper("one")
	id := h.comment(board.User, task, api.CommentRequest{Body: "@developer please"})
	h.notifyNow()
	want := []string{fmt.Sprintf("#%d comment %d by user mentions @developer: omo ai comments %d --since %d", task, id, task, id-1)}
	if got := h.agent(dev.ID).typed(); !slices.Equal(got, want) {
		t.Fatalf("typed %q, want %q", got, want)
	}
	h.notifyNow()
	if got := h.agent(dev.ID).typed(); len(got) != 1 {
		t.Fatalf("a second pass delivered again: %q", got)
	}
}

func TestTypedLineWaitsForTheGracePeriod(t *testing.T) {
	h := newHarness(t)
	h.e.nt.grace = 20 * time.Millisecond
	task, dev := h.readyDeveloper("one")
	h.comment(board.User, task, api.CommentRequest{Body: "@developer please"})
	h.notifyNow()
	h.waitFor("the line to be typed", func() bool { return len(h.agent(dev.ID).typed()) == 1 })
}

func TestMentionReachesEveryLiveReviewer(t *testing.T) {
	// A story with three projects has one reviewer per project, the one of
	// c kept from round 1; the board records only one session per
	// mentioned role.
	v := routeView{
		tickets: map[int64]ticketInfo{1: {id: 1, typ: api.TypeStory, column: api.ColumnReview}},
		sessions: []board.EngineSession{
			{ID: "rev-a", Role: api.RoleReviewer, Ticket: 1, Project: "a", ReviewRound: 2},
			{ID: "rev-b", Role: api.RoleReviewer, Ticket: 1, Project: "b", ReviewRound: 2},
			{ID: "rev-old", Role: api.RoleReviewer, Ticket: 1, Project: "a", ReviewRound: 1},
			{ID: "rev-c", Role: api.RoleReviewer, Ticket: 1, Project: "c", ReviewRound: 1}, // kept: c's approval carried over
			{ID: "pm", Role: api.RolePM, Ticket: 1},
		},
	}
	r := route(commentEvent(10, api.Comment{ID: 5, Ticket: 1, Author: api.Actor{Role: api.RolePM, Session: "pm", Name: "Pat"},
		Mentions: []api.Mention{{Role: api.RoleReviewer, Session: "rev-a"}}}), v)
	if got := targets(r); !slices.Equal(got, []string{"rev-a", "rev-b", "rev-c"}) {
		t.Fatalf("notified %v, want the newest reviewer of each project", got)
	}
	if r.deliveries[0].line != "omo: #1 comment 5 by Pat mentions @reviewer: omo ai comments 1 --since 4" {
		t.Fatalf("line %q", r.deliveries[0].line)
	}
}

func TestAuthorIsNotNotifiedOfOwnMention(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	task, dev := h.readyDeveloper("one")
	h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "note for @developer"})
	h.notifyNow()
	if got := h.agent(dev.ID).typed(); len(got) != 0 {
		t.Fatalf("the author was notified: %q", got)
	}
	if r, err := h.e.Wait(h.ctx, h.actor(dev.ID), time.Millisecond); err != nil || !strings.HasPrefix(r, "timeout:") {
		t.Fatalf("wait = %q, %v; want a timeout (nothing queued)", r, err)
	}
}

func TestNotificationDuringStartIsInReadyPrompt(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	id := h.comment(board.User, task, api.CommentRequest{Body: "@developer read this first"})
	h.notifyNow()
	if got := h.agent(dev.ID).typed(); len(got) != 0 {
		t.Fatalf("typed into an agent that has not started: %q", got)
	}
	p, err := h.e.Ready(h.ctx, h.actor(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("\n\nNotifications that arrived while you started:\nomo: #%d comment %d by user mentions @developer", task, id)
	if !strings.Contains(p, want) {
		t.Fatalf("ready prompt lacks the notification:\n%s", p)
	}
	if !strings.HasPrefix(p, "You are "+dev.Name+", the developer of task #") {
		t.Fatalf("ready prompt does not start with the role prompt:\n%s", p)
	}
	if st := h.sessionState(dev.ID); st != api.SessionRunning {
		t.Fatalf("state %s after ready", st)
	}
}

func TestNotificationBeforeWaitIsNotLost(t *testing.T) {
	h := newHarness(t)
	h.e.nt.grace = time.Hour // the line is not typed before the wait comes
	task, dev := h.readyDeveloper("one")
	id := h.comment(board.User, task, api.CommentRequest{Body: "@developer here"})
	h.notifyNow()
	r, err := h.e.Wait(h.ctx, h.actor(dev.ID), time.Hour)
	if err != nil || !strings.Contains(r, fmt.Sprintf("comment %d by user", id)) {
		t.Fatalf("wait = %q, %v", r, err)
	}
	h.e.flush(dev.ID) // the grace period ends: nothing is left to type
	if got := h.agent(dev.ID).typed(); len(got) != 0 {
		t.Fatalf("a line returned by the wait was typed too: %q", got)
	}
}

func TestQuestionAnsweredWakesAsker(t *testing.T) {
	h := newHarness(t)
	task, dev := h.readyDeveloper("one")
	q := h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "which way?", Question: true})
	h.notifyNow()
	ch := h.park(h.ctx, dev.ID, time.Hour)
	a := h.comment(board.User, task, api.CommentRequest{Body: "this way", ReplyTo: &q})
	h.notifyNow()
	r := h.result(ch)
	want := fmt.Sprintf("omo: your question %d on #%d was answered (comment %d): omo ai comments %d --since %d", q, task, a, task, a-1)
	if r.err != nil || r.reason != want {
		t.Fatalf("wait = %q, %v; want %q", r.reason, r.err, want)
	}
}

func TestAnswerThatMentionsAskerArrivesOnce(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	task, dev := h.readyDeveloper("one")
	q := h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "which way?", Question: true})
	h.comment(board.User, task, api.CommentRequest{Body: "@developer this way", ReplyTo: &q})
	h.notifyNow()
	if got := h.agent(dev.ID).typed(); len(got) != 1 || !strings.Contains(got[0], "mentions @developer") {
		t.Fatalf("typed %q, want the mention once", got)
	}
}

func TestQuestionToPMNotifiesPM(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	s := h.story("Story")
	h.approve(s)
	h.step()
	pm := h.live(api.RolePM, s)
	if _, err := h.e.Ready(h.ctx, h.actor(pm.ID)); err != nil {
		t.Fatal(err)
	}
	task := h.taskAs(h.actor(pm.ID), s, "task")
	h.approveAs(h.actor(pm.ID), task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	q := h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "which way?", Question: true})
	h.notifyNow()
	want := fmt.Sprintf("#%d question %d for you from %s: omo ai comments %d --questions", task, q, dev.Name, task)
	if got := h.agent(pm.ID).typed(); !slices.Contains(got, want) {
		t.Fatalf("pm got %q, want %q", got, want)
	}
}

func TestTaskDoneNotifiesPMAndAllDoneHint(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	s := h.story("Story")
	h.approve(s)
	h.step()
	pm := h.live(api.RolePM, s)
	if _, err := h.e.Ready(h.ctx, h.actor(pm.ID)); err != nil {
		t.Fatal(err)
	}
	t1, t2 := h.task(s, "one"), h.task(s, "two")
	for _, n := range []int64{t1, t2} {
		if _, err := h.b.Cancel(h.ctx, board.User, n, api.CancelRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	h.notifyNow()
	got := h.agent(pm.ID).typed()
	first := fmt.Sprintf("task #%d of story #%d is done (cancelled).", t1, s)
	last := fmt.Sprintf("task #%d of story #%d is done (cancelled). Every task of story #%d is done or cancelled; when the story is complete, run omo ai done.", t2, s, s)
	if !slices.Contains(got, first) || !slices.Contains(got, last) {
		t.Fatalf("pm got %q\nwant %q and %q", got, first, last)
	}
}

func TestRetryOfStalledAgentRestartsIt(t *testing.T) {
	h := newHarness(t)
	task, dev := h.readyDeveloper("one")
	h.notifyNow()
	if err := h.b.SetFailed(h.ctx, task, api.FailedAgent, "no output"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.b.Retry(h.ctx, board.User, task); err != nil {
		t.Fatal(err)
	}
	h.notifyNow()
	h.e.settle()
	if got := h.waitEnded(dev.ID); got.EndReason != "restarted by user" {
		t.Fatalf("end reason %q", got.EndReason)
	}
	if !h.agent(dev.ID).isKilled() {
		t.Fatal("the stalled agent was not killed")
	}
	h.step()
	if fresh := h.live(api.RoleDeveloper, task); fresh.ID == dev.ID {
		t.Fatal("no fresh developer")
	}
}

func TestNotificationIsDeliveredExactlyOnceWhileWaitsComeAndGo(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	_, dev := h.readyDeveloper("one")
	a := h.actor(dev.ID)
	const n = 200
	var mu sync.Mutex
	var got []string
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			r, err := h.e.Wait(h.ctx, a, time.Microsecond)
			if err != nil {
				t.Error(err)
				return
			}
			if !strings.HasPrefix(r, "timeout:") {
				mu.Lock()
				got = append(got, strings.Split(r, "\n")...)
				mu.Unlock()
			}
		}
	}()
	for i := range n {
		h.e.notify(dev.ID, fmt.Sprintf("omo: line %d", i))
	}
	close(stop)
	<-done
	for _, l := range h.agent(dev.ID).typed() {
		got = append(got, linePrefix+l)
	}
	seen := map[string]int{}
	for _, l := range got {
		seen[l]++
	}
	for i := range n {
		if c := seen[fmt.Sprintf("omo: line %d", i)]; c != 1 {
			t.Fatalf("line %d delivered %d times", i, c)
		}
	}
	if len(got) != n {
		t.Fatalf("%d deliveries, want %d", len(got), n)
	}
}

// ---- routing table ----

func commentEvent(id int64, c api.Comment) api.Event {
	data, _ := json.Marshal(api.CommentCreatedData{Comment: c})
	n := c.Ticket
	return api.Event{ID: id, Kind: api.KindCommentCreated, Ticket: &n, Actor: c.Author, Data: data}
}

func movedEvent(id int64, actor api.Actor, s api.TicketSummary, to api.Column, action string) api.Event {
	data, _ := json.Marshal(api.TicketMovedData{Ticket: s, To: to, Action: action})
	n := s.Number
	return api.Event{ID: id, Kind: api.KindTicketMoved, Ticket: &n, Actor: actor, Data: data}
}

func targets(r routing) []string {
	var out []string
	for _, d := range r.deliveries {
		out = append(out, d.session)
	}
	return out
}

func TestRouteTable(t *testing.T) {
	story, task, standalone := int64(1), int64(2), int64(3)
	sid := 1
	view := func() routeView {
		return routeView{
			tickets: map[int64]ticketInfo{
				story:      {id: story, typ: api.TypeStory, column: api.ColumnInProgress},
				task:       {id: task, story: story, typ: api.TypeTask, column: api.ColumnReview},
				4:          {id: 4, story: story, typ: api.TypeTask, column: api.ColumnInProgress},
				standalone: {id: standalone, typ: api.TypeTask, column: api.ColumnInProgress},
			},
			sessions: []board.EngineSession{
				{ID: "pm", Role: api.RolePM, Ticket: story},
				{ID: "dev", Role: api.RoleDeveloper, Ticket: task, Purpose: api.PurposeWork},
				{ID: "export", Role: api.RoleDeveloper, Ticket: task, Purpose: api.PurposeExport, Project: "a"},
				{ID: "rev", Role: api.RoleReviewer, Ticket: task, ReviewRound: 1},
				{ID: "rev-old", Role: api.RoleReviewer, Ticket: task, ReviewRound: 0},
				{ID: "dev3", Role: api.RoleDeveloper, Ticket: standalone},
				{ID: "late", Role: api.RoleDeveloper, Ticket: task},
				{ID: "ceo", Role: api.RoleCEO},
			},
			sessionBornAt: map[string]int64{"late": 100},
			asker: func(id int64) (board.EngineSession, bool) {
				s, ok := map[int64]board.EngineSession{
					7:  {ID: "dev", Role: api.RoleDeveloper, Ticket: task, Purpose: api.PurposeWork},
					8:  {ID: "gone", Role: api.RoleDeveloper, Ticket: standalone + 10, Purpose: api.PurposeWork},
					11: {ID: "gone", Role: api.RoleDeveloper, Ticket: standalone, Purpose: api.PurposeWork},
					13: {ID: "gone", Role: api.RoleDeveloper, Ticket: task, Purpose: api.PurposeWork},
				}[id]
				return s, ok
			},
			ceoEnabled: true,
		}
	}
	noCEO := func(v routeView) routeView {
		v.sessions = slices.DeleteFunc(slices.Clone(v.sessions), func(s board.EngineSession) bool { return s.Role == api.RoleCEO })
		return v
	}
	user := api.Actor{Role: api.RoleUser}
	devActor := api.Actor{Role: api.RoleDeveloper, Session: "dev", Name: "Dana"}
	ceoActor := api.Actor{Role: api.RoleCEO, Session: "ceo", Name: "Cleo"}
	system := api.Actor{Role: api.RoleSystem}
	mention := func(roles ...api.Role) []api.Mention {
		var out []api.Mention
		for _, r := range roles {
			out = append(out, api.Mention{Role: r})
		}
		return out
	}
	taskDone := api.TicketSummary{Number: int(task), Type: api.TypeTask, Story: &sid, Supervisor: api.RolePM, Resolution: api.ResolutionCompleted}
	storyCEO := api.TicketSummary{Number: int(story), Type: api.TypeStory, Supervisor: api.RoleCEO, Resolution: api.ResolutionCompleted}
	storyUser := storyCEO
	storyUser.Supervisor = api.RoleUser

	for _, tc := range []struct {
		name     string
		ev       api.Event
		v        func(routeView) routeView
		want     []string
		line     string // of the first delivery, when set
		ceo      int
		startCEO bool
		restart  int64
		// restartSessions are the sessions a restart ends.
		restartSessions []string
		ended           string
	}{
		{name: "comment without mention", ev: commentEvent(10, api.Comment{ID: 1, Ticket: int(task), Author: user})},
		{name: "@developer reaches the working developer, not the export job nor a later session",
			ev:   commentEvent(10, api.Comment{ID: 1, Ticket: int(task), Author: user, Mentions: mention(api.RoleDeveloper)}),
			want: []string{"dev"}, line: "omo: #2 comment 1 by user mentions @developer: omo ai comments 2 --since 0"},
		{name: "@reviewer reaches the newest reviewer of each project",
			ev:   commentEvent(10, api.Comment{ID: 1, Ticket: int(task), Author: user, Mentions: mention(api.RoleReviewer)}),
			want: []string{"rev"}},
		{name: "@reviewer reaches a reviewer kept from an earlier round",
			ev: commentEvent(10, api.Comment{ID: 1, Ticket: int(task), Author: user, Mentions: mention(api.RoleReviewer)}),
			v: func(v routeView) routeView {
				v.sessions = slices.DeleteFunc(slices.Clone(v.sessions), func(s board.EngineSession) bool { return s.ID == "rev" })
				return v
			},
			want: []string{"rev-old"}},
		{name: "@pm on a task reaches the story's pm",
			ev:   commentEvent(10, api.Comment{ID: 1, Ticket: int(task), Author: devActor, Mentions: mention(api.RolePM)}),
			want: []string{"pm"}, line: "omo: #2 comment 1 by Dana mentions @pm: omo ai comments 2 --since 0"},
		{name: "several roles, each once",
			ev: commentEvent(10, api.Comment{ID: 1, Ticket: int(task), Author: user,
				Mentions: mention(api.RolePM, api.RoleReviewer, api.RolePM, api.RoleUser)}),
			want: []string{"pm", "rev"}},
		{name: "the author's own session is skipped",
			ev:   commentEvent(10, api.Comment{ID: 1, Ticket: int(task), Author: devActor, Mentions: mention(api.RoleDeveloper, api.RoleReviewer)}),
			want: []string{"rev"}},
		{name: "a system comment carries its own mention (rejection, description change, attention)",
			ev:   commentEvent(10, api.Comment{ID: 4, Ticket: int(task), Author: system, Mentions: mention(api.RoleDeveloper)}),
			want: []string{"dev"}, line: "omo: #2 comment 4 by system mentions @developer: omo ai comments 2 --since 3"},
		{name: "a question reaches its addressee with the question line",
			ev: commentEvent(10, api.Comment{ID: 6, Ticket: int(task), Author: devActor, QuestionTo: api.RolePM,
				Mentions: mention(api.RolePM)}),
			want: []string{"pm"}, line: "omo: #2 question 6 for you from Dana: omo ai comments 2 --questions"},
		{name: "a question to the user reaches no agent",
			ev: commentEvent(10, api.Comment{ID: 6, Ticket: int(standalone), Author: api.Actor{Role: api.RoleDeveloper, Session: "dev3"},
				QuestionTo: api.RoleUser, Mentions: mention(api.RoleUser)})},
		{name: "@ceo reaches a running CEO",
			ev:   commentEvent(10, api.Comment{ID: 1, Ticket: int(standalone), Author: user, Mentions: mention(api.RoleCEO)}),
			want: []string{"ceo"}},
		{name: "@ceo starts a CEO that is not running",
			ev: commentEvent(10, api.Comment{ID: 1, Ticket: int(standalone), Author: user, Mentions: mention(api.RoleCEO)}),
			v:  noCEO, ceo: 1, startCEO: true},
		{name: "a question to the CEO starts it",
			ev: commentEvent(10, api.Comment{ID: 1, Ticket: int(story), Author: api.Actor{Role: api.RolePM, Session: "pm"},
				QuestionTo: api.RoleCEO, Mentions: mention(api.RoleCEO)}),
			v: noCEO, ceo: 1, startCEO: true},
		{name: "@ceo while paused keeps the line but starts nothing",
			ev: commentEvent(10, api.Comment{ID: 1, Ticket: int(standalone), Author: user, Mentions: mention(api.RoleCEO)}),
			v:  func(v routeView) routeView { v = noCEO(v); v.paused = true; return v }, ceo: 1},
		{name: "@ceo while the CEO is switched off does nothing",
			ev: commentEvent(10, api.Comment{ID: 1, Ticket: int(standalone), Author: user, Mentions: mention(api.RoleCEO)}),
			v:  func(v routeView) routeView { v = noCEO(v); v.ceoEnabled = false; return v }},
		{name: "the CEO's own mention of itself",
			ev: commentEvent(10, api.Comment{ID: 1, Ticket: int(standalone), Author: ceoActor, Mentions: mention(api.RoleCEO)})},
		{name: "an answer wakes the asker",
			ev: func() api.Event {
				data, _ := json.Marshal(api.QuestionAnsweredData{Ticket: int(task), Question: 7, Answer: 12})
				return api.Event{ID: 10, Kind: api.KindQuestionAnswered, Actor: user, Data: data}
			}(),
			want: []string{"dev"}, line: "omo: your question 7 on #2 was answered (comment 12): omo ai comments 2 --since 11"},
		{name: "an answer to an asker that ended or was the user reaches nobody",
			ev: func() api.Event {
				data, _ := json.Marshal(api.QuestionAnsweredData{Ticket: int(task), Question: 8, Answer: 12})
				return api.Event{ID: 10, Kind: api.KindQuestionAnswered, Actor: user, Data: data}
			}()},
		{name: "an answer to an asker that ended reaches the live agent of its slot",
			ev: func() api.Event {
				data, _ := json.Marshal(api.QuestionAnsweredData{Ticket: int(standalone), Question: 11, Answer: 12})
				return api.Event{ID: 10, Kind: api.KindQuestionAnswered, Actor: user, Data: data}
			}(),
			want: []string{"dev3"}},
		{name: "the slot's agent must have existed at the answer",
			ev: func() api.Event {
				data, _ := json.Marshal(api.QuestionAnsweredData{Ticket: int(task), Question: 13, Answer: 12})
				return api.Event{ID: 10, Kind: api.KindQuestionAnswered, Actor: user, Data: data}
			}(),
			// "dev" fills the slot; "late" was born after the answer.
			want: []string{"dev"}},
		{name: "a done task tells its pm; another task is still open",
			ev:   movedEvent(10, system, taskDone, api.ColumnDone, board.ActionMerged),
			want: []string{"pm"}, line: "omo: task #2 of story #1 is done (completed)."},
		{name: "the last done task adds the hint",
			ev: movedEvent(10, system, taskDone, api.ColumnDone, board.ActionMerged),
			v: func(v routeView) routeView {
				v.tickets[4] = ticketInfo{id: 4, story: story, typ: api.TypeTask, column: api.ColumnDone}
				return v
			},
			want: []string{"pm"},
			line: "omo: task #2 of story #1 is done (completed). Every task of story #1 is done or cancelled; when the story is complete, run omo ai done."},
		{name: "a task done later in the batch still counts as open",
			ev: movedEvent(10, system, taskDone, api.ColumnDone, board.ActionMerged),
			v: func(v routeView) routeView {
				v.tickets[4] = ticketInfo{id: 4, story: story, typ: api.TypeTask, column: api.ColumnDone}
				v.doneAt = map[int64]int64{4: 11}
				return v
			},
			want: []string{"pm"}, line: "omo: task #2 of story #1 is done (completed)."},
		{name: "a task the snapshot no longer lists, done later in the batch, still counts as open",
			ev: movedEvent(10, system, taskDone, api.ColumnDone, board.ActionMerged),
			v: func(v routeView) routeView {
				delete(v.tickets, 4)
				v.doneAt = map[int64]int64{4: 11}
				v.doneTasks = map[int64]int64{4: story}
				return v
			},
			want: []string{"pm"}, line: "omo: task #2 of story #1 is done (completed)."},
		{name: "a pm cancelling its own task is not told",
			ev: movedEvent(10, api.Actor{Role: api.RolePM, Session: "pm"}, taskDone, api.ColumnDone, board.ActionCancel)},
		{name: "a done ticket the CEO supervises tells a running CEO",
			ev:   movedEvent(10, system, storyCEO, api.ColumnDone, board.ActionMerged),
			want: []string{"ceo"}, line: "omo: #1 is done (completed)."},
		{name: "a done ticket does not start the CEO",
			ev: movedEvent(10, system, storyCEO, api.ColumnDone, board.ActionMerged), v: noCEO},
		{name: "a done ticket the user supervises tells no CEO",
			ev: movedEvent(10, system, storyUser, api.ColumnDone, board.ActionMerged)},
		{name: "a rejected review the CEO supervises tells a running CEO",
			ev:   movedEvent(10, api.Actor{Role: api.RoleReviewer, Session: "rev"}, storyCEO, api.ColumnInProgress, board.ActionReject),
			want: []string{"ceo"}, line: "omo: the review of #1 was rejected."},
		{name: "other moves tell nobody",
			ev: movedEvent(10, user, storyCEO, api.ColumnReady, board.ActionApprove)},
		{name: "a retry of the agent step restarts the agents",
			ev: func() api.Event {
				data, _ := json.Marshal(api.TicketRetriedData{Ticket: int(task), Step: api.FailedAgent})
				return api.Event{ID: 10, Kind: api.KindTicketRetried, Actor: api.Actor{Role: api.RolePM, Session: "pm"}, Data: data}
			}(),
			// "late" was started after the retry, as the fresh agent.
			restart: task, restartSessions: []string{"dev", "export", "rev", "rev-old"}},
		{name: "a retry of another step restarts nothing",
			ev: func() api.Event {
				data, _ := json.Marshal(api.TicketRetriedData{Ticket: int(task), Step: api.FailedMerge})
				return api.Event{ID: 10, Kind: api.KindTicketRetried, Actor: user, Data: data}
			}()},
		{name: "an ended session",
			ev: func() api.Event {
				data, _ := json.Marshal(api.AgentData{Agent: api.AgentSession{ID: "dev"}})
				return api.Event{ID: 10, Kind: api.KindAgentEnded, Actor: system, Data: data}
			}(),
			ended: "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := view()
			if tc.v != nil {
				v = tc.v(v)
			}
			r := route(tc.ev, v)
			if got := targets(r); !slices.Equal(got, tc.want) {
				t.Fatalf("notified %v, want %v", got, tc.want)
			}
			if tc.line != "" && r.deliveries[0].line != tc.line {
				t.Fatalf("line %q\nwant %q", r.deliveries[0].line, tc.line)
			}
			if len(r.ceo) != tc.ceo || r.startCEO != tc.startCEO {
				t.Fatalf("CEO lines %d start %v, want %d %v", len(r.ceo), r.startCEO, tc.ceo, tc.startCEO)
			}
			if r.restart != tc.restart || r.ended != tc.ended {
				t.Fatalf("restart %d ended %q", r.restart, r.ended)
			}
			if !slices.Equal(r.restartSessions, tc.restartSessions) {
				t.Fatalf("restart sessions %v, want %v", r.restartSessions, tc.restartSessions)
			}
			for _, d := range r.deliveries {
				if !strings.HasPrefix(d.line, linePrefix) || strings.ContainsAny(d.line, "\n\r") {
					t.Fatalf("line %q", d.line)
				}
			}
		})
	}
}

func TestRetryDoesNotEndTheFreshAgentStartedForIt(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	for range maxFailures {
		dev := h.live(api.RoleDeveloper, task)
		h.agent(dev.ID).exit(errors.New("exit status 1"))
		h.waitEnded(dev.ID)
		h.step()
	}
	if f := h.ticket(task).FailedStep; f != api.FailedAgent {
		t.Fatalf("failed step %q", f)
	}
	h.notifyNow()
	if _, err := h.b.Retry(h.ctx, board.User, task); err != nil {
		t.Fatal(err)
	}
	h.step() // the pass starts the fresh agent before the retry is routed
	fresh := h.live(api.RoleDeveloper, task)
	h.notifyNow()
	h.e.settle()
	h.step()
	if got := h.live(api.RoleDeveloper, task); got.ID != fresh.ID {
		t.Fatal("the retry ended the fresh agent started for it")
	}
	if n := len(h.runner.all()); n != maxFailures+1 {
		t.Fatalf("%d agents started, want %d", n, maxFailures+1)
	}
}

func TestAnswerAfterRestartReachesTheAgentInTheAskersSlot(t *testing.T) {
	h := newHarness(t)
	task, dev := h.readyDeveloper("one")
	q := h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "which way?", Question: true})
	h.notifyNow()

	h.restart()
	last, err := h.b.LastEventID(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	h.e.nt.cursor = last // where a new run's notifier starts
	h.step()
	fresh := h.live(api.RoleDeveloper, task)
	if fresh.ID == dev.ID {
		t.Fatal("no fresh developer after the restart")
	}
	if _, err := h.e.Ready(h.ctx, h.actor(fresh.ID)); err != nil {
		t.Fatal(err)
	}
	ch := h.park(h.ctx, fresh.ID, time.Hour)
	a := h.comment(board.User, task, api.CommentRequest{Body: "this way", ReplyTo: &q})
	h.notifyNow()
	r := h.result(ch)
	want := fmt.Sprintf("omo: your question %d on #%d was answered (comment %d): omo ai comments %d --since %d", q, task, a, task, a-1)
	if r.err != nil || r.reason != want {
		t.Fatalf("wait = %q, %v; want %q", r.reason, r.err, want)
	}
}
