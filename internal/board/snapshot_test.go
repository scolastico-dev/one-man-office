package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

func snapshot(t *testing.T, b *Board) EngineSnapshot {
	t.Helper()
	s, err := b.EngineSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func snapTicket(s EngineSnapshot, n int) (EngineTicket, bool) {
	for _, t := range s.Tickets {
		if t.ID == int64(n) {
			return t, true
		}
	}
	return EngineTicket{}, false
}

func TestEngineSnapshotContents(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()

	empty := snapshot(t, b)
	if empty.Tickets == nil || empty.Sessions == nil || len(empty.Tickets) != 0 || empty.Paused {
		t.Fatalf("empty snapshot %+v", empty)
	}

	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}, MaxParallel: intPtr(2)}).Number

	// A done task that still records its worktree, one without.
	withTree := standaloneTask(t, b)
	if err := b.SetBranch(ctx, int64(withTree), "api", "omo/2-t", "/wt/api/2"); err != nil {
		t.Fatal(err)
	}
	force(t, b, withTree, api.ColumnDone, "")
	finished := standaloneTask(t, b)
	force(t, b, finished, api.ColumnDone, "")

	// An exported task whose pull request is not merged, and one that is.
	exported := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "E", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport}).Number
	force(t, b, exported, api.ColumnReview, api.ReviewStageExporting)
	if err := b.CompleteExport(ctx, int64(exported), "https://example.test/pr/1"); err != nil {
		t.Fatal(err)
	}
	mergedExport := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "M", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport}).Number
	force(t, b, mergedExport, api.ColumnReview, api.ReviewStageExporting)
	if err := b.CompleteExport(ctx, int64(mergedExport), "https://example.test/pr/2"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.MarkMerged(ctx, User, int64(mergedExport)); err != nil {
		t.Fatal(err)
	}

	// A cancelled task that still records its worktree.
	cancelled := standaloneTask(t, b)
	if err := b.SetBranch(ctx, int64(cancelled), "api", "omo/7-t", "/wt/api/7"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cancel(ctx, User, int64(cancelled), api.CancelRequest{DeleteBranch: true}); err != nil {
		t.Fatal(err)
	}

	// A reviewed task in its second round: the first round's rejection
	// does not count.
	reviewed := standaloneTask(t, b)
	force(t, b, reviewed, api.ColumnInProgress, "")
	dev := agent(t, b, api.RoleDeveloper, int64(reviewed))
	handIn := func() {
		t.Helper()
		if _, err := b.Done(ctx, dev, api.DoneRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	handIn()
	rev := reviewer(t, b, reviewed, "api")
	if _, err := b.Done(ctx, rev, api.DoneRequest{Verdict: VerdictReject, Comment: "@developer fix"}); err != nil {
		t.Fatal(err)
	}
	handIn()
	// The first round's reviewer ends; the second round has its own.
	if err := b.EndSession(ctx, rev.Session, "review round over"); err != nil {
		t.Fatal(err)
	}
	rev = reviewer(t, b, reviewed, "api")

	// A ready task waiting for a planned one, created by the CEO, which is
	// switched off below.
	ceo := agent(t, b, api.RoleCEO, 0)
	dep := standaloneTask(t, b)
	waiting := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "W", Projects: []string{"api"}, DependsOn: []int{dep}, Model: "claude/fable"}).Number
	force(t, b, waiting, api.ColumnReady, "")
	if err := b.SetStartBlocked(ctx, map[int64]api.StartBlocked{int64(waiting): api.StartBlockedSlot}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })

	s := snapshot(t, b)
	if !s.Paused || s.LastEventID != lastEvent(t, b) {
		t.Fatalf("paused %v last event %d", s.Paused, s.LastEventID)
	}
	var ids []int64
	for _, tk := range s.Tickets {
		ids = append(ids, tk.ID)
	}
	want := []int64{int64(story), int64(withTree), int64(exported), int64(cancelled), int64(reviewed), int64(dep), int64(waiting)}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Fatalf("tickets %v, want %v", ids, want)
	}

	st, _ := snapTicket(s, story)
	if st.Type != api.TypeStory || st.Column != api.ColumnPlanning || st.MaxParallel != 2 || st.Supervisor != api.RoleUser ||
		st.Review != api.ReviewAgent || len(st.Projects) != 2 || st.Projects[0].ID != "api" || st.Projects[1].ID != "ui" ||
		st.Projects[0].Base != "main" || st.Verdicts == nil || st.DependsOn == nil || st.WaitingFor == nil {
		t.Fatalf("story %+v", st)
	}
	wt, _ := snapTicket(s, withTree)
	if wt.Column != api.ColumnDone || wt.Resolution != api.ResolutionCompleted ||
		wt.Projects[0] != (EngineProject{ID: "api", Base: "main", Branch: "omo/2-t", Worktree: "/wt/api/2"}) {
		t.Fatalf("done task %+v", wt)
	}
	ex, _ := snapTicket(s, exported)
	if ex.Column != api.ColumnReview || ex.ReviewStage != api.ReviewStageAwaitingMerge || ex.Resolution != "" || ex.ExportMerged || ex.WhenApproved != api.WhenApprovedExport {
		t.Fatalf("exported %+v", ex)
	}
	cn, _ := snapTicket(s, cancelled)
	if cn.Resolution != api.ResolutionCancelled || !cn.DeleteBranch || cn.DiscardUncommitted {
		t.Fatalf("cancelled %+v", cn)
	}
	rv, _ := snapTicket(s, reviewed)
	if rv.Column != api.ColumnReview || rv.ReviewStage != api.ReviewStageAgent || rv.ReviewRound != 2 || len(rv.Verdicts) != 0 {
		t.Fatalf("reviewed %+v", rv)
	}
	w, _ := snapTicket(s, waiting)
	if !w.Waiting || fmt.Sprint(w.DependsOn) != fmt.Sprint([]int64{int64(dep)}) || len(w.WaitingFor) != 1 ||
		w.WaitingFor[0] != (api.Waiting{Ticket: dep, Reason: api.WaitingNotDone}) ||
		w.Supervisor != api.RoleUser || w.Model != "claude/fable" || w.StartBlocked != api.StartBlockedSlot {
		t.Fatalf("waiting %+v", w)
	}

	if len(s.Sessions) != 3 {
		t.Fatalf("sessions %+v", s.Sessions)
	}
	d := s.Sessions[0]
	if d.ID != dev.Session || d.Role != api.RoleDeveloper || d.Ticket != int64(reviewed) || d.Project != "api" ||
		d.Purpose != api.PurposeWork || d.State != api.SessionStarting || d.Name != "developer-agent" ||
		d.Model != "claude/fable" || d.StartedAt.IsZero() {
		t.Fatalf("developer session %+v", d)
	}
	if s.Sessions[1].ID != rev.Session || s.Sessions[1].ReviewRound != 2 || s.Sessions[2].ID != ceo.Session || s.Sessions[2].Ticket != 0 {
		t.Fatalf("sessions %+v", s.Sessions)
	}

	// The second round's approval is the only verdict.
	if _, err := b.Done(ctx, rev, api.DoneRequest{Verdict: VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	if err := b.EndSession(ctx, ceo.Session, "test"); err != nil {
		t.Fatal(err)
	}
	s = snapshot(t, b)
	rv, _ = snapTicket(s, reviewed)
	if len(rv.Verdicts) != 1 || rv.Verdicts["api"] != VerdictApprove || rv.ReviewStage != api.ReviewStageMerging {
		t.Fatalf("verdicts %v stage %q", rv.Verdicts, rv.ReviewStage)
	}
	if len(s.Sessions) != 2 {
		t.Fatalf("ended session still listed: %+v", s.Sessions)
	}
}

// countingQueryer counts the statements run through it.
type countingQueryer struct {
	q db.Queryer
	n int
}

func (c *countingQueryer) Exec(query string, args ...any) (sql.Result, error) {
	c.n++
	return c.q.Exec(query, args...)
}

func (c *countingQueryer) Query(query string, args ...any) (*sql.Rows, error) {
	c.n++
	return c.q.Query(query, args...)
}

func (c *countingQueryer) QueryRow(query string, args ...any) *sql.Row {
	c.n++
	return c.q.QueryRow(query, args...)
}

// snapshotQueries returns the number of statements one snapshot runs.
func snapshotQueries(t *testing.T, b *Board) (int, EngineSnapshot) {
	t.Helper()
	var n int
	var s EngineSnapshot
	err := b.read(context.Background(), func(tx *sql.Tx) error {
		c := &countingQueryer{q: tx}
		var err error
		s, err = b.engineSnapshot(c)
		n = c.n
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n, s
}

func TestEngineSnapshotQueryCountIsFixed(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	// world adds a story with tasks, dependencies, a review verdict, live
	// sessions and a cancelled task that keeps its worktree.
	world := func() {
		s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}}).Number
		force(t, b, s, api.ColumnInProgress, "")
		agent(t, b, api.RolePM, int64(s))
		prev := 0
		for i := 0; i < 3; i++ {
			req := api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s)}
			if prev != 0 {
				req.DependsOn = []int{prev}
			}
			prev = mustCreate(t, b, User, req).Number
		}
		force(t, b, prev, api.ColumnInProgress, "")
		dev := agent(t, b, api.RoleDeveloper, int64(prev))
		if _, err := b.Done(ctx, dev, api.DoneRequest{}); err != nil {
			t.Fatal(err)
		}
		rev := reviewer(t, b, prev, "api")
		if _, err := b.Done(ctx, rev, api.DoneRequest{Verdict: VerdictApprove}); err != nil {
			t.Fatal(err)
		}
		c := standaloneTask(t, b)
		if err := b.SetBranch(ctx, int64(c), "api", fmt.Sprintf("omo/%d-t", c), fmt.Sprintf("/wt/%d", c)); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Cancel(ctx, User, int64(c), api.CancelRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	world()
	small, s1 := snapshotQueries(t, b)
	for i := 0; i < 10; i++ {
		world()
	}
	large, s2 := snapshotQueries(t, b)
	if len(s2.Tickets) <= len(s1.Tickets)*5 || len(s2.Sessions) <= len(s1.Sessions)*5 {
		t.Fatalf("the world did not grow: %d -> %d tickets", len(s1.Tickets), len(s2.Tickets))
	}
	if small != large {
		t.Fatalf("%d queries for %d tickets, %d for %d", small, len(s1.Tickets), large, len(s2.Tickets))
	}
	t.Logf("%d queries", small)
}

func TestEventsAfterReturnsStreamedKindsInOrder(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	start, err := b.LastEventID(ctx)
	if err != nil || start != lastEvent(t, b) {
		t.Fatalf("last event %d, %v", start, err)
	}
	n := standaloneTask(t, b)
	if _, err := db.AppendEvent(b.db, "internal", `{"role":"system"}`, 0, `{}`); err != nil {
		t.Fatal(err)
	}
	dev := agent(t, b, api.RoleDeveloper, int64(n))
	if err := b.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}

	evs, err := b.EventsAfter(ctx, start, 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var prev int64
	for _, e := range evs {
		if e.ID <= prev {
			t.Fatalf("not in order: %d after %d", e.ID, prev)
		}
		prev = e.ID
		kinds = append(kinds, e.Kind)
	}
	wantKinds := []string{api.KindTicketCreated, api.KindAgentCreated, api.KindTicketUpdated, api.KindServerPaused}
	if fmt.Sprint(kinds) != fmt.Sprint(wantKinds) {
		t.Fatalf("kinds %v, want %v", kinds, wantKinds)
	}
	if evs[0].Ticket == nil || *evs[0].Ticket != n || evs[3].Ticket != nil || evs[0].At == "" {
		t.Fatalf("events %+v", evs)
	}
	if !strings.Contains(string(evs[1].Data), dev.Session) {
		t.Fatalf("session id hidden from the engine: %s", evs[1].Data)
	}
	var created api.TicketCreatedData
	if err := json.Unmarshal(evs[0].Data, &created); err != nil || created.Ticket.Number != n {
		t.Fatalf("data %s, %v", evs[0].Data, err)
	}
	if last, _ := b.LastEventID(ctx); last != prev {
		t.Fatalf("last event %d, want %d", last, prev)
	}

	two, err := b.EventsAfter(ctx, start, 2)
	if err != nil || len(two) != 2 || two[1].ID != evs[1].ID {
		t.Fatalf("limit: %+v, %v", two, err)
	}
	none, err := b.EventsAfter(ctx, prev, 10)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("after the last: %+v, %v", none, err)
	}
}

func TestOpenQuestionsAndSessionCommentedAfter(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	qs, err := b.OpenQuestions(ctx)
	if err != nil || qs == nil || len(qs) != 0 {
		t.Fatalf("questions %+v, %v", qs, err)
	}
	if id, err := b.LastCommentID(ctx); err != nil || id != 0 {
		t.Fatalf("last comment %d, %v", id, err)
	}
	story, task := storyWithTask(t, b)
	force(t, b, task, api.ColumnInProgress, "")
	pm := agent(t, b, api.RolePM, int64(story))
	dev := agent(t, b, api.RoleDeveloper, int64(task))
	other := standaloneTask(t, b)

	q := ask(t, b, dev, task, "")
	userQ := ask(t, b, User, other, "ceo")
	qs, err = b.OpenQuestions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 2 {
		t.Fatalf("questions %+v", qs)
	}
	got := qs[0]
	if got.Comment != int64(q) || got.Ticket != int64(task) || got.To != api.RolePM || got.AuthorRole != api.RoleDeveloper ||
		got.AuthorSession != dev.Session || got.CreatedAt.IsZero() {
		t.Fatalf("question %+v", got)
	}
	if qs[1].Comment != int64(userQ) || qs[1].AuthorRole != api.RoleUser || qs[1].AuthorSession != "" || qs[1].To != api.RoleCEO {
		t.Fatalf("user's question %+v", qs[1])
	}

	c, err := b.CommentByID(ctx, int64(q))
	if err != nil || c.Author.Session != dev.Session || len(c.Mentions) != 1 || c.Mentions[0] != (api.Mention{Role: api.RolePM, Session: pm.Session}) {
		t.Fatalf("comment %+v, %v", c, err)
	}
	_, err = b.CommentByID(ctx, 999)
	wantCode(t, err, api.CodeNotFound)

	// The pm answers: the question is no longer open.
	if _, err := b.Comment(ctx, pm, int64(task), api.CommentRequest{Body: "do A", ReplyTo: intPtr(q)}); err != nil {
		t.Fatal(err)
	}
	if qs, _ = b.OpenQuestions(ctx); len(qs) != 1 || qs[0].Comment != int64(userQ) {
		t.Fatalf("questions after the answer %+v", qs)
	}

	mark, err := b.LastCommentID(ctx)
	if err != nil || mark == 0 {
		t.Fatalf("last comment %d, %v", mark, err)
	}
	commented := func(a Actor, n int) bool {
		t.Helper()
		ok, err := b.SessionCommentedAfter(ctx, a.Session, int64(n), mark)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if commented(dev, task) || commented(pm, task) {
		t.Fatal("commented before writing")
	}
	if _, err := b.Comment(ctx, dev, int64(task), api.CommentRequest{Body: "handing over: A is half done"}); err != nil {
		t.Fatal(err)
	}
	if !commented(dev, task) || commented(pm, task) || commented(dev, story) {
		t.Fatal("wrong answer after the handoff comment")
	}
	if last, _ := b.LastCommentID(ctx); last <= mark {
		t.Fatalf("last comment %d not after %d", last, mark)
	}
}
