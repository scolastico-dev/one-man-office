package board

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// written drops the system comments, such as the one telling the pm that
// the user added a task.
func written(cs []api.Comment) []api.Comment {
	out := []api.Comment{}
	for _, c := range cs {
		if c.Author.Role != api.RoleSystem {
			out = append(out, c)
		}
	}
	return out
}

// storyWithTask creates a story and a task inside it and returns their
// numbers.
func storyWithTask(t *testing.T, b *Board) (story, task int) {
	t.Helper()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})
	return s.Number, tk.Number
}

func TestUnreachableRoles(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	_, task := storyWithTask(t, b)
	before := lastEvent(t, b)
	res, err := b.Comment(ctx, User, int64(task), api.CommentRequest{Body: "@developer @ceo @user @pm @reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	want := []api.Role{api.RoleDeveloper, api.RolePM, api.RoleReviewer}
	if !slices.Equal(res.Unreachable, want) {
		t.Fatalf("Unreachable = %v, want %v", res.Unreachable, want)
	}
	for _, m := range res.Comment.Mentions {
		if m.Session != "" {
			t.Fatalf("mention %+v has a session", m)
		}
	}
	if len(res.Comment.Mentions) != 5 {
		t.Fatalf("mentions = %+v", res.Comment.Mentions)
	}
	cs, err := b.Comments(ctx, User, int64(task))
	if err != nil {
		t.Fatal(err)
	}
	if cs = written(cs); len(cs) != 1 || cs[0].ID != res.Comment.ID {
		t.Fatalf("comment not stored: %+v", cs)
	}

	// The event carries the mentions with their sessions for the engine.
	dev := agent(t, b, api.RoleDeveloper, int64(task))
	before = lastEvent(t, b)
	res, err = b.Comment(ctx, User, int64(task), api.CommentRequest{Body: "@developer again"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unreachable) != 0 {
		t.Fatalf("Unreachable = %v", res.Unreachable)
	}
	evs := eventsAfter(t, b, before)
	if len(evs) != 1 || evs[0].Kind != api.KindCommentCreated {
		t.Fatalf("events = %+v", evs)
	}
	var data api.CommentCreatedData
	if err := json.Unmarshal([]byte(evs[0].Detail), &data); err != nil {
		t.Fatal(err)
	}
	if want := []api.Mention{{Role: api.RoleDeveloper, Session: dev.Session}}; !slices.Equal(data.Comment.Mentions, want) {
		t.Fatalf("event mentions = %+v, want %+v", data.Comment.Mentions, want)
	}
}

func TestCommentWithoutMentionNotifiesNobody(t *testing.T) {
	b, _ := newTestBoard(t)
	_, task := storyWithTask(t, b)
	agent(t, b, api.RoleDeveloper, int64(task))
	res, err := b.Comment(context.Background(), User, int64(task), api.CommentRequest{Body: "  just a note  "})
	if err != nil {
		t.Fatal(err)
	}
	if res.Comment.Body != "just a note" || len(res.Comment.Mentions) != 0 || res.Comment.Mentions == nil {
		t.Fatalf("comment = %+v", res.Comment)
	}
}

func TestCommentValidation(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	story, task := storyWithTask(t, b)
	other, err := b.Comment(ctx, User, int64(story), api.CommentRequest{Body: "on the story"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Comment(ctx, User, int64(task), api.CommentRequest{Body: "   "})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Comment(ctx, User, int64(task), api.CommentRequest{Body: "x", To: "pm"})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Comment(ctx, User, int64(task), api.CommentRequest{Body: "x", ReplyTo: &other.Comment.ID})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Comment(ctx, User, int64(task), api.CommentRequest{Body: "x", ReplyTo: intPtr(999)})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Comment(ctx, User, 999, api.CommentRequest{Body: "x"})
	wantCode(t, err, api.CodeNotFound)
	_, err = b.Comment(ctx, User, int64(task), api.CommentRequest{Body: "x", Question: true, To: "developer"})
	wantCode(t, err, api.CodeValidation)
}

func TestCommentWriteScope(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	story, task := storyWithTask(t, b)
	other, otherTask := storyWithTask(t, b)
	sibling := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "Sib", Projects: []string{"api"}, Story: intPtr(story)})
	dev := agent(t, b, api.RoleDeveloper, int64(task))
	pm := agent(t, b, api.RolePM, int64(story))
	ceo := agent(t, b, api.RoleCEO, 0)
	say := func(a Actor, n int) error {
		_, err := b.Comment(ctx, a, int64(n), api.CommentRequest{Body: "hello"})
		return err
	}
	wantCode(t, say(dev, sibling.Number), api.CodeForbidden)
	wantCode(t, say(dev, story), api.CodeForbidden)
	wantCode(t, say(pm, otherTask), api.CodeForbidden)
	wantCode(t, say(pm, other), api.CodeForbidden)
	for _, n := range []int{story, task, other, otherTask} {
		if err := say(ceo, n); err != nil {
			t.Errorf("ceo on #%d: %v", n, err)
		}
	}
	forceDone(t, b, task, api.ResolutionCompleted)
	if err := say(dev, task); err != nil {
		t.Errorf("comment on a done ticket: %v", err)
	}
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	wantCode(t, say(ceo, story), api.CodeForbidden)
}

func TestQuestionDefaultsToSupervisorAndCountsOpen(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	story, task := storyWithTask(t, b)
	pm := agent(t, b, api.RolePM, int64(story))
	dev := agent(t, b, api.RoleDeveloper, int64(task))
	before := lastEvent(t, b)
	res, err := b.Comment(ctx, dev, int64(task), api.CommentRequest{Body: "which API?", Question: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Comment.QuestionTo != api.RolePM || res.Comment.AnsweredBy != nil {
		t.Fatalf("question = %+v", res.Comment)
	}
	// The asking developer is not shown the pm's session id; the user is.
	if want := []api.Mention{{Role: api.RolePM}}; !slices.Equal(res.Comment.Mentions, want) {
		t.Fatalf("mentions = %+v", res.Comment.Mentions)
	}
	if cs, err := b.Comments(ctx, User, int64(task)); err != nil ||
		!slices.Equal(cs[len(cs)-1].Mentions, []api.Mention{{Role: api.RolePM, Session: pm.Session}}) {
		t.Fatalf("stored mentions = %+v, %v", cs, err)
	}
	got := get(t, b, task)
	if got.OpenQuestions != 1 || got.QuestionsForUser != 0 {
		t.Fatalf("open %d, for user %d", got.OpenQuestions, got.QuestionsForUser)
	}
	if !slices.Contains(derivedUpdates(t, eventsAfter(t, b, before)), task) {
		t.Fatal("no derived ticket.updated for the question")
	}
	// The pm's "needs me" view finds it.
	list, err := b.List(ctx, pm, api.TicketFilter{NeedsMe: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(numbers(list.Tickets), []int{task}) {
		t.Fatalf("pm needs_me = %v", numbers(list.Tickets))
	}

	// A question to the user counts for the user and shows in their view.
	if _, err := b.Comment(ctx, dev, int64(task), api.CommentRequest{Body: "may I?", Question: true, To: ToUser}); err != nil {
		t.Fatal(err)
	}
	got = get(t, b, task)
	if got.OpenQuestions != 2 || got.QuestionsForUser != 1 {
		t.Fatalf("open %d, for user %d", got.OpenQuestions, got.QuestionsForUser)
	}
	list, err = b.List(ctx, User, api.TicketFilter{NeedsMe: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(numbers(list.Tickets), task) {
		t.Fatalf("user needs_me = %v", numbers(list.Tickets))
	}
}

func TestQuestionAddressees(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	story, task := storyWithTask(t, b)
	alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
	pm := agent(t, b, api.RolePM, int64(story))
	ask := func(a Actor, n int, to string) (api.CommentResult, error) {
		return b.Comment(ctx, a, int64(n), api.CommentRequest{Body: "?", Question: true, To: to})
	}
	// The pm supervises the task, so its own question goes up to the
	// story's supervisor.
	res, err := ask(pm, task, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Comment.QuestionTo != api.RoleUser {
		t.Fatalf("pm question to %q, want user", res.Comment.QuestionTo)
	}
	_, err = ask(pm, task, ToPM)
	wantCode(t, err, api.CodeValidation)
	_, err = ask(User, alone.Number, ToPM)
	wantCode(t, err, api.CodeInvalidMention)
	_, err = ask(User, alone.Number, "")
	wantCode(t, err, api.CodeValidation)
	_, err = ask(User, alone.Number, "nobody")
	wantCode(t, err, api.CodeValidation)

	// A pm without a live session is unreachable as an addressee.
	if err := b.EndSession(ctx, pm.Session, "done"); err != nil {
		t.Fatal(err)
	}
	res, err = ask(User, task, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Comment.QuestionTo != api.RolePM || !slices.Equal(res.Unreachable, []api.Role{api.RolePM}) {
		t.Fatalf("result = %+v", res)
	}

	// The CEO is never unreachable; it is started on demand.
	res, err = ask(User, alone.Number, ToCEO)
	if err != nil {
		t.Fatal(err)
	}
	if res.Comment.QuestionTo != api.RoleCEO || len(res.Unreachable) != 0 {
		t.Fatalf("result = %+v", res)
	}
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	_, err = ask(User, alone.Number, ToCEO)
	wantCode(t, err, api.CodeInvalidMention)
}

func TestQuestionAnsweredOnlyByAddresseeOrUser(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	story, task := storyWithTask(t, b)
	pm := agent(t, b, api.RolePM, int64(story))
	dev := agent(t, b, api.RoleDeveloper, int64(task))
	rev := agent(t, b, api.RoleReviewer, int64(task))
	q, err := b.Comment(ctx, dev, int64(task), api.CommentRequest{Body: "which API?", Question: true})
	if err != nil {
		t.Fatal(err)
	}
	reply := func(a Actor, to int) api.CommentResult {
		t.Helper()
		res, err := b.Comment(ctx, a, int64(task), api.CommentRequest{Body: "this one", ReplyTo: &to})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	reply(rev, q.Comment.ID)
	reply(dev, q.Comment.ID)
	if got := get(t, b, task); got.OpenQuestions != 1 {
		t.Fatalf("question closed by a reply of someone else: open %d", got.OpenQuestions)
	}
	before := lastEvent(t, b)
	answer := reply(pm, q.Comment.ID)
	cs, err := b.Comments(ctx, User, int64(task))
	if err != nil {
		t.Fatal(err)
	}
	cs = written(cs)
	if cs[0].AnsweredBy == nil || *cs[0].AnsweredBy != answer.Comment.ID {
		t.Fatalf("question = %+v", cs[0])
	}
	if got := get(t, b, task); got.OpenQuestions != 0 {
		t.Fatalf("open %d after the answer", got.OpenQuestions)
	}
	var answered bool
	for _, e := range eventsAfter(t, b, before) {
		if e.Kind == api.KindQuestionAnswered {
			var d api.QuestionAnsweredData
			if err := json.Unmarshal([]byte(e.Detail), &d); err != nil {
				t.Fatal(err)
			}
			answered = d == api.QuestionAnsweredData{Ticket: task, Question: q.Comment.ID, Answer: answer.Comment.ID}
		}
	}
	if !answered {
		t.Fatal("no matching question.answered event")
	}
	// A second answer does not move answered_by.
	reply(User, q.Comment.ID)
	cs, _ = b.Comments(ctx, User, int64(task))
	cs = written(cs)
	if *cs[0].AnsweredBy != answer.Comment.ID {
		t.Fatalf("answered_by moved to %d", *cs[0].AnsweredBy)
	}

	// The user answers any question.
	q2, err := b.Comment(ctx, dev, int64(task), api.CommentRequest{Body: "and?", Question: true})
	if err != nil {
		t.Fatal(err)
	}
	userAnswer := reply(User, q2.Comment.ID)
	cs, _ = b.Comments(ctx, User, int64(task))
	cs = written(cs)
	if last := cs[len(cs)-2]; last.ID != q2.Comment.ID || last.AnsweredBy == nil || *last.AnsweredBy != userAnswer.Comment.ID {
		t.Fatalf("question 2 = %+v", last)
	}
}

func TestCommentsOldestFirstWithMentions(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	_, task := storyWithTask(t, b)
	for _, body := range []string{"one @pm", "two", "three @user @developer"} {
		if _, err := b.Comment(ctx, User, int64(task), api.CommentRequest{Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	cs, err := b.Comments(ctx, User, int64(task))
	if err != nil {
		t.Fatal(err)
	}
	if cs = written(cs); len(cs) != 3 || cs[0].Body != "one @pm" || cs[2].Body != "three @user @developer" {
		t.Fatalf("comments = %+v", cs)
	}
	if len(cs[0].Mentions) != 1 || cs[1].Mentions == nil || len(cs[1].Mentions) != 0 || len(cs[2].Mentions) != 2 ||
		cs[2].Mentions[0].Role != api.RoleUser || cs[2].Mentions[1].Role != api.RoleDeveloper {
		t.Fatalf("mentions = %+v / %+v / %+v", cs[0].Mentions, cs[1].Mentions, cs[2].Mentions)
	}
	empty, err := b.Comments(ctx, User, int64(task-1))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("comments of the story = %v, %v", empty, err)
	}
}

func TestReassignCEO(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	ceo := agent(t, b, api.RoleCEO, 0)
	story := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
	quiet := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "Q", Projects: []string{"api"}})
	q, err := b.Comment(ctx, User, int64(alone.Number), api.CommentRequest{Body: "?", Question: true, To: ToCEO})
	if err != nil {
		t.Fatal(err)
	}
	// A question that also mentions the user, an answered question and a
	// plain comment to the CEO.
	both, err := b.Comment(ctx, ceo, int64(story.Number), api.CommentRequest{Body: "@user and @ceo?", Question: true, To: ToUser})
	if err != nil {
		t.Fatal(err)
	}
	dev := agent(t, b, api.RoleDeveloper, int64(alone.Number))
	withUser, err := b.Comment(ctx, dev, int64(alone.Number), api.CommentRequest{Body: "@user too?", Question: true, To: ToCEO})
	if err != nil {
		t.Fatal(err)
	}
	answered, err := b.Comment(ctx, dev, int64(alone.Number), api.CommentRequest{Body: "done?", Question: true, To: ToCEO})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Comment(ctx, ceo, int64(alone.Number), api.CommentRequest{Body: "yes", ReplyTo: &answered.Comment.ID}); err != nil {
		t.Fatal(err)
	}
	note, err := b.Comment(ctx, User, int64(alone.Number), api.CommentRequest{Body: "fyi @ceo"})
	if err != nil {
		t.Fatal(err)
	}
	ceoMention := []api.Mention{{Role: api.RoleCEO, Session: ceo.Session}}
	if get(t, b, story.Number).Supervisor != api.RoleCEO {
		t.Fatal("story not supervised by the CEO")
	}
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	before := lastEvent(t, b)
	if err := b.ReassignCEO(ctx); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, story.Number); got.Supervisor != api.RoleUser {
		t.Fatalf("story supervisor = %q", got.Supervisor)
	}
	if got := get(t, b, alone.Number); got.QuestionsForUser != 2 {
		t.Fatalf("questions for user = %d", got.QuestionsForUser)
	}
	cs, _ := b.Comments(ctx, User, int64(alone.Number))
	if cs[0].ID != q.Comment.ID || cs[0].QuestionTo != api.RoleUser {
		t.Fatalf("question = %+v", cs[0])
	}
	mentions := map[int][]api.Mention{}
	for _, n := range []int{alone.Number, story.Number} {
		cs, err := b.Comments(ctx, User, int64(n))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			mentions[c.ID] = c.Mentions
		}
	}
	userMention := []api.Mention{{Role: api.RoleUser}}
	for id, want := range map[int][]api.Mention{
		q.Comment.ID:        userMention,
		withUser.Comment.ID: userMention,
		answered.Comment.ID: ceoMention,
		note.Comment.ID:     ceoMention,
		// Addressed to the user already: untouched.
		both.Comment.ID: {{Role: api.RoleUser}, {Role: api.RoleCEO, Session: ceo.Session}},
	} {
		if !slices.Equal(mentions[id], want) {
			t.Errorf("mentions of comment %d = %+v, want %+v", id, mentions[id], want)
		}
	}
	updated := map[int][]string{}
	for _, e := range eventsAfter(t, b, before) {
		if e.Kind != api.KindTicketUpdated {
			t.Fatalf("unexpected event %s", e.Kind)
		}
		var d api.TicketUpdatedData
		if err := json.Unmarshal([]byte(e.Detail), &d); err != nil {
			t.Fatal(err)
		}
		if _, dup := updated[int(e.TicketID)]; dup {
			t.Fatalf("two updates for #%d", e.TicketID)
		}
		updated[int(e.TicketID)] = d.Fields
	}
	if len(updated) != 2 || !slices.Equal(updated[story.Number], []string{"supervisor"}) ||
		!slices.Equal(updated[alone.Number], []string{fieldDerived}) {
		t.Fatalf("updates = %v", updated)
	}
	if _, ok := updated[quiet.Number]; ok {
		t.Fatal("untouched ticket updated")
	}
}

func TestTicketEventsIncludeMoves(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	other := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "O", Projects: []string{"api"}})
	if _, err := b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Approve(ctx, User, int64(other.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	evs, err := b.TicketEvents(ctx, User, int64(tk.Number))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 || evs[0].Kind != api.KindTicketCreated {
		t.Fatalf("events = %+v", evs)
	}
	var moved bool
	for i, e := range evs {
		if e.Ticket == nil || *e.Ticket != tk.Number {
			t.Fatalf("event of another ticket: %+v", e)
		}
		if i > 0 && e.ID <= evs[i-1].ID {
			t.Fatal("events not oldest first")
		}
		if e.Kind == api.KindTicketMoved {
			var d api.TicketMovedData
			if err := json.Unmarshal(e.Data, &d); err != nil {
				t.Fatal(err)
			}
			moved = d.Action == ActionApprove && e.Actor.Role == api.RoleUser && e.At != ""
		}
	}
	if !moved {
		t.Fatalf("no ticket.moved approve event in %+v", evs)
	}
}

func TestSystemCommentsGoThroughTheCommentPath(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	ceo := agent(t, b, api.RoleCEO, 0)
	tk := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	// The supervisor is still ceo; with the CEO off its mention goes to the
	// user, and the reason text is not searched for mentions.
	if err := b.SetAttention(ctx, int64(tk.Number), "stalled, @pm said"); err != nil {
		t.Fatal(err)
	}
	cs, err := b.Comments(ctx, User, int64(tk.Number))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Author.Role != api.RoleSystem || !slices.Equal(cs[0].Mentions, []api.Mention{{Role: api.RoleUser}}) {
		t.Fatalf("comments = %+v", cs)
	}
}

// checkQuestionsClosed checks that the questions qs of ticket n are answered
// by a system comment, that question.answered was emitted for each after
// last, and that the ticket counts no open question.
func checkQuestionsClosed(t *testing.T, b *Board, n int, last int64, qs ...int) {
	t.Helper()
	cs, err := b.Comments(context.Background(), User, int64(n))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int]api.Comment{}
	for _, c := range cs {
		byID[c.ID] = c
	}
	answered := map[int]int{}
	for _, e := range eventsAfter(t, b, last) {
		if e.Kind != api.KindQuestionAnswered || e.TicketID != int64(n) {
			continue
		}
		var d api.QuestionAnsweredData
		if err := json.Unmarshal([]byte(e.Detail), &d); err != nil {
			t.Fatal(err)
		}
		if d.Ticket != n || !strings.Contains(e.Actor, `"role":"system"`) {
			t.Fatalf("question.answered %+v by %s", d, e.Actor)
		}
		answered[d.Question] = d.Answer
	}
	for _, q := range qs {
		c := byID[q]
		if c.AnsweredBy == nil {
			t.Fatalf("question %d of #%d is still open", q, n)
		}
		a := byID[*c.AnsweredBy]
		if a.Author.Role != api.RoleSystem || !strings.Contains(a.Body, "closed without an answer") {
			t.Fatalf("question %d answered by %+v", q, a)
		}
		if answered[q] != a.ID {
			t.Fatalf("question.answered for %d = %d, want %d", q, answered[q], a.ID)
		}
	}
	if s := get(t, b, n); s.OpenQuestions != 0 || s.QuestionsForUser != 0 {
		t.Fatalf("#%d open questions %d, for user %d", n, s.OpenQuestions, s.QuestionsForUser)
	}
}

func ask(t *testing.T, b *Board, a Actor, n int, to string) int {
	t.Helper()
	res, err := b.Comment(context.Background(), a, int64(n), api.CommentRequest{Body: "why?", Question: true, To: to})
	if err != nil {
		t.Fatal(err)
	}
	return res.Comment.ID
}

func TestCancelClosesOpenQuestions(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	ceo := agent(t, b, api.RoleCEO, 0)
	n := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}}).Number
	q1 := ask(t, b, ceo, n, ToUser)
	q2 := ask(t, b, User, n, ToCEO)
	needsMe := func() []int {
		list, err := b.List(ctx, User, api.TicketFilter{NeedsMe: true})
		if err != nil {
			t.Fatal(err)
		}
		return numbers(list.Tickets)
	}
	if got := needsMe(); !slices.Equal(got, []int{n}) {
		t.Fatalf("needs me before cancel = %v", got)
	}
	last := lastEvent(t, b)
	if _, err := b.Cancel(ctx, User, int64(n), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	checkQuestionsClosed(t, b, n, last, q1, q2)
	if got := needsMe(); len(got) != 0 {
		t.Fatalf("needs me after cancel = %v", got)
	}
	// The ticket.moved summary already counts no open question.
	if moved := movedEvents(t, eventsAfter(t, b, last)); len(moved) != 1 || moved[0].Ticket.OpenQuestions != 0 {
		t.Fatalf("moved = %+v", moved)
	}
	// A question asked on the closed ticket stays open.
	ask(t, b, ceo, n, ToUser)
	if s := get(t, b, n); s.OpenQuestions != 1 || s.QuestionsForUser != 1 {
		t.Fatalf("after a new question: open %d, for user %d", s.OpenQuestions, s.QuestionsForUser)
	}
}

func TestDoneClosesOpenQuestions(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	ceo := agent(t, b, api.RoleCEO, 0)
	n := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}}).Number
	q := ask(t, b, ceo, n, ToUser)
	force(t, b, n, api.ColumnReview, api.ReviewStageMerging)
	last := lastEvent(t, b)
	if err := b.CompleteMerge(ctx, int64(n)); err != nil {
		t.Fatal(err)
	}
	checkQuestionsClosed(t, b, n, last, q)
}

func TestStoryCancelClosesQuestionsOfItsTasks(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}}).Number
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story)}).Number
	pm := agent(t, b, api.RolePM, int64(story))
	qs := ask(t, b, pm, story, ToSupervisor)
	qt := ask(t, b, User, task, ToPM)
	last := lastEvent(t, b)
	if _, err := b.Cancel(ctx, User, int64(story), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	checkQuestionsClosed(t, b, story, last, qs)
	checkQuestionsClosed(t, b, task, last, qt)
}
