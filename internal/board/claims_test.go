package board

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// updatedFields returns, per ticket.updated event of evs, the ticket number
// and its field list joined by commas.
func updatedFields(t *testing.T, evs []db.Event) []string {
	t.Helper()
	out := []string{}
	for _, e := range evs {
		if e.Kind != api.KindTicketUpdated {
			continue
		}
		var d api.TicketUpdatedData
		if err := json.Unmarshal([]byte(e.Detail), &d); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("#%d %s", d.Ticket.Number, strings.Join(d.Fields, ",")))
	}
	return out
}

func standaloneTask(t *testing.T, b *Board) int {
	t.Helper()
	return mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}}).Number
}

func TestClaimStartChecksStateAndSetsBusy(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	force(t, b, s.Number, api.ColumnReady, "")
	if _, err := b.db.Exec(`UPDATE tickets SET start_blocked = 'slot' WHERE id = ?`, s.Number); err != nil {
		t.Fatal(err)
	}
	last := lastEvent(t, b)
	if err := b.ClaimStart(ctx, int64(s.Number)); err != nil {
		t.Fatal(err)
	}
	got := get(t, b, s.Number)
	if !got.Busy || got.StartBlocked != "" || got.Column != api.ColumnReady {
		t.Fatalf("busy %v start_blocked %q column %s", got.Busy, got.StartBlocked, got.Column)
	}
	if f := updatedFields(t, eventsAfter(t, b, last)); fmt.Sprint(f) != fmt.Sprintf("[#%d busy,start_blocked]", s.Number) {
		t.Fatalf("updates %v", f)
	}
	err := b.ClaimStart(ctx, int64(s.Number))
	wantCode(t, err, api.CodeConflict)

	planned := standaloneTask(t, b)
	wantCode(t, b.ClaimStart(ctx, int64(planned)), api.CodeInvalidTransition)

	dep := standaloneTask(t, b)
	waiting := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "W", Projects: []string{"api"}, DependsOn: []int{dep}}).Number
	force(t, b, waiting, api.ColumnReady, "")
	wantCode(t, b.ClaimStart(ctx, int64(waiting)), api.CodeInvalidTransition)

	// A task of a story starts only while the story is in progress.
	inStory := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "I", Projects: []string{"api"}, Story: intPtr(s.Number)}).Number
	force(t, b, inStory, api.ColumnReady, "")
	wantCode(t, b.ClaimStart(ctx, int64(inStory)), api.CodeInvalidTransition)

	failed := standaloneTask(t, b)
	force(t, b, failed, api.ColumnReady, "")
	if err := b.SetFailed(ctx, int64(failed), api.FailedStart, "branch gone"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, b.ClaimStart(ctx, int64(failed)), api.CodeInvalidTransition)
	wantCode(t, b.ClaimStart(ctx, 999), api.CodeNotFound)
}

func TestClaimMergeRules(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := standaloneTask(t, b)
	for _, st := range allStates {
		if st == (boardState{api.ColumnReview, api.ReviewStageMerging}) {
			continue
		}
		force(t, b, n, st.col, st.stage)
		wantCode(t, b.ClaimMerge(ctx, int64(n)), api.CodeInvalidTransition)
	}
	force(t, b, n, api.ColumnReview, api.ReviewStageMerging)
	if err := b.ClaimMerge(ctx, int64(n)); err != nil {
		t.Fatal(err)
	}
	if !get(t, b, n).Busy {
		t.Fatal("not busy")
	}
	wantCode(t, b.ClaimMerge(ctx, int64(n)), api.CodeConflict)
	if err := b.SetFailed(ctx, int64(n), api.FailedMerge, "dirty checkout"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, b.ClaimMerge(ctx, int64(n)), api.CodeInvalidTransition)
}

func TestBeginWorkReleasesClaim(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := standaloneTask(t, b)
	force(t, b, n, api.ColumnReady, "")
	if err := b.ClaimStart(ctx, int64(n)); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginWork(ctx, int64(n)); err != nil {
		t.Fatal(err)
	}
	got := get(t, b, n)
	if got.Column != api.ColumnInProgress || got.Busy {
		t.Fatalf("column %s busy %v", got.Column, got.Busy)
	}
}

func TestCompleteMergeAndConflictReleaseClaim(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	for _, conflict := range []bool{false, true} {
		n := standaloneTask(t, b)
		force(t, b, n, api.ColumnReview, api.ReviewStageMerging)
		if err := b.ClaimMerge(ctx, int64(n)); err != nil {
			t.Fatal(err)
		}
		var err error
		if conflict {
			err = b.MergeConflict(ctx, int64(n), "conflict in a.go")
		} else {
			err = b.CompleteMerge(ctx, int64(n))
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := get(t, b, n); got.Busy {
			t.Fatalf("conflict %v: still busy in %s", conflict, got.Column)
		}
	}
}

func TestCancelRefusedWhileMergeClaimed(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := standaloneTask(t, b)
	force(t, b, n, api.ColumnReview, api.ReviewStageMerging)
	if err := b.ClaimMerge(ctx, int64(n)); err != nil {
		t.Fatal(err)
	}
	_, err := b.Cancel(ctx, User, int64(n), api.CancelRequest{DeleteBranch: true})
	wantCode(t, err, api.CodeConflict)
	if api.StatusFor(errCode(err)) != 409 || !strings.Contains(err.Error(), "merging") {
		t.Fatalf("error %v", err)
	}
	if got := get(t, b, n); got.Column != api.ColumnReview || !got.Busy {
		t.Fatalf("refused cancel changed the ticket: %s busy %v", got.Column, got.Busy)
	}
	if err := b.CompleteMerge(ctx, int64(n)); err != nil {
		t.Fatal(err)
	}
	_, err = b.Cancel(ctx, User, int64(n), api.CancelRequest{})
	wantCode(t, err, api.CodeInvalidTransition)
}

func TestStopStoryRefusedWhileTaskClaimed(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}}).Number
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s)}).Number
	force(t, b, s, api.ColumnInProgress, "")
	force(t, b, task, api.ColumnReview, api.ReviewStageMerging)
	if err := b.ClaimMerge(ctx, int64(task)); err != nil {
		t.Fatal(err)
	}
	for _, to := range []api.Column{api.ColumnReady, api.ColumnPlanning} {
		_, err := b.Move(ctx, User, int64(s), api.MoveRequest{Column: to})
		wantCode(t, err, api.CodeConflict)
		if !strings.Contains(err.Error(), fmt.Sprintf("merging #%d", task)) {
			t.Fatalf("error %v", err)
		}
	}
	_, err := b.Cancel(ctx, User, int64(s), api.CancelRequest{})
	wantCode(t, err, api.CodeConflict)
	if err := b.ReleaseClaim(ctx, int64(task)); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Move(ctx, User, int64(s), api.MoveRequest{Column: api.ColumnReady}); err != nil || got.Column != api.ColumnReady {
		t.Fatalf("after release: %s, %v", got.Column, err)
	}
}

func TestWithdrawRefusedWhileStartClaimed(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := standaloneTask(t, b)
	if _, err := b.Approve(ctx, User, int64(n), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := b.ClaimStart(ctx, int64(n)); err != nil {
		t.Fatal(err)
	}
	_, err := b.Move(ctx, User, int64(n), api.MoveRequest{Column: api.ColumnPlanning})
	wantCode(t, err, api.CodeConflict)
	if !strings.Contains(err.Error(), "starting work on") {
		t.Fatalf("error %v", err)
	}
	// Reordering within the column is still allowed.
	if _, err := b.Move(ctx, User, int64(n), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseClaimAndRecoveryAfterCrash(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	ready, merging, idle := standaloneTask(t, b), standaloneTask(t, b), standaloneTask(t, b)
	force(t, b, ready, api.ColumnReady, "")
	force(t, b, merging, api.ColumnReview, api.ReviewStageMerging)
	if err := b.ClaimStart(ctx, int64(ready)); err != nil {
		t.Fatal(err)
	}
	if err := b.ClaimMerge(ctx, int64(merging)); err != nil {
		t.Fatal(err)
	}

	// Releasing an unclaimed ticket changes nothing and notifies nobody.
	last, notified := lastEvent(t, b), env.notifications()
	if err := b.ReleaseClaim(ctx, int64(idle)); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, b, last); len(evs) != 0 || env.notifications() != notified {
		t.Fatalf("events %+v, notifications %d -> %d", evs, notified, env.notifications())
	}
	wantCode(t, b.ReleaseClaim(ctx, 999), api.CodeNotFound)

	// omo ends between claim and outcome: the claims are still stored, and
	// the next start releases them.
	b2 := New(b.db, env, testOptions(env))
	if !get(t, b2, ready).Busy || !get(t, b2, merging).Busy {
		t.Fatal("claims not stored")
	}
	last = lastEvent(t, b2)
	if err := b2.ReleaseAllClaims(ctx); err != nil {
		t.Fatal(err)
	}
	if got := updatedFields(t, eventsAfter(t, b2, last)); fmt.Sprint(got) != fmt.Sprintf("[#%d busy #%d busy]", ready, merging) {
		t.Fatalf("updates %v", got)
	}
	if get(t, b2, ready).Busy || get(t, b2, merging).Busy {
		t.Fatal("still busy")
	}
	// The released tickets can be claimed again, and the user may act on
	// them in between.
	if err := b2.ClaimMerge(ctx, int64(merging)); err != nil {
		t.Fatal(err)
	}
	if _, err := b2.Move(ctx, User, int64(ready), api.MoveRequest{Column: api.ColumnPlanning}); err != nil {
		t.Fatal(err)
	}
	last, notified = lastEvent(t, b2), env.notifications()
	if err := b2.ReleaseAllClaims(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b2.ReleaseAllClaims(ctx); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, b2, last); len(evs) != 1 || env.notifications() != notified+1 {
		t.Fatalf("events %+v, notifications %d -> %d", evs, notified, env.notifications())
	}
}

func TestSetFailedFlagsAndCommentsSupervisor(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	ceo := agent(t, b, api.RoleCEO, 0)
	n := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}}).Number
	force(t, b, n, api.ColumnReady, "")
	if _, err := b.db.Exec(`UPDATE tickets SET start_blocked = 'slot' WHERE id = ?`, n); err != nil {
		t.Fatal(err)
	}
	if err := b.ClaimStart(ctx, int64(n)); err != nil {
		t.Fatal(err)
	}
	last := lastEvent(t, b)
	if err := b.SetFailed(ctx, int64(n), api.FailedStart, "branch gone"); err != nil {
		t.Fatal(err)
	}
	got := get(t, b, n)
	if got.FailedStep != api.FailedStart || got.Attention != "branch gone" || got.Busy || got.StartBlocked != "" {
		t.Fatalf("failed %q attention %q busy %v start_blocked %q", got.FailedStep, got.Attention, got.Busy, got.StartBlocked)
	}
	if f := updatedFields(t, eventsAfter(t, b, last)); fmt.Sprint(f) != fmt.Sprintf("[#%d failed_step,attention,busy]", n) {
		t.Fatalf("updates %v", f)
	}
	c, _ := lastComment(t, b, n)
	if c.Author.Role != api.RoleSystem || c.Body != "Needs attention: branch gone" || len(c.Mentions) != 1 ||
		c.Mentions[0] != (api.Mention{Role: api.RoleCEO, Session: ceo.Session}) {
		t.Fatalf("comment %+v", c)
	}

	// The same step and reason again: nothing happens.
	last = lastEvent(t, b)
	if err := b.SetFailed(ctx, int64(n), api.FailedStart, "branch gone"); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, b, last); len(evs) != 0 {
		t.Fatalf("events %+v", evs)
	}

	wantCode(t, b.SetFailed(ctx, int64(n), "bogus", "x"), api.CodeValidation)
	wantCode(t, b.SetFailed(ctx, int64(n), api.FailedNone, "x"), api.CodeValidation)
	wantCode(t, b.SetFailed(ctx, int64(n), api.FailedAgent, " "), api.CodeValidation)
	force(t, b, n, api.ColumnDone, "")
	wantCode(t, b.SetFailed(ctx, int64(n), api.FailedAgent, "late"), api.CodeInvalidTransition)
}

func TestClearFailedOnlyClearsItsStep(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := standaloneTask(t, b)
	force(t, b, n, api.ColumnInProgress, "")
	if err := b.SetFailed(ctx, int64(n), api.FailedAgent, "stalled"); err != nil {
		t.Fatal(err)
	}
	last := lastEvent(t, b)
	if err := b.ClearFailed(ctx, int64(n), api.FailedStart); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, n); got.FailedStep != api.FailedAgent || len(eventsAfter(t, b, last)) != 0 {
		t.Fatalf("other step cleared: %q", got.FailedStep)
	}
	if err := b.ClearFailed(ctx, int64(n), api.FailedAgent); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, n); got.FailedStep != "" || got.Attention != "" {
		t.Fatalf("failed %q attention %q", got.FailedStep, got.Attention)
	}
	if f := updatedFields(t, eventsAfter(t, b, last)); fmt.Sprint(f) != fmt.Sprintf("[#%d failed_step,attention]", n) {
		t.Fatalf("updates %v", f)
	}
}

func TestColumnChangeClearsFailedStep(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := standaloneTask(t, b)
	force(t, b, n, api.ColumnInProgress, "")
	if err := b.SetFailed(ctx, int64(n), api.FailedAgent, "crashed three times"); err != nil {
		t.Fatal(err)
	}
	got, err := b.Move(ctx, User, int64(n), api.MoveRequest{Column: api.ColumnReady})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnReady || got.FailedStep != "" || got.Attention != "" {
		t.Fatalf("column %s failed %q attention %q", got.Column, got.FailedStep, got.Attention)
	}
}

func TestRetryPermissionsAndEffect(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}}).Number
	force(t, b, s, api.ColumnInProgress, "")
	pm := agent(t, b, api.RolePM, int64(s))
	task := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s)}).Number
	force(t, b, task, api.ColumnInProgress, "")
	dev := agent(t, b, api.RoleDeveloper, int64(task))
	ceo := agent(t, b, api.RoleCEO, 0)
	usersTask := standaloneTask(t, b)
	ceosTask := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "C", Projects: []string{"api"}}).Number
	for _, n := range []int{s, task, usersTask, ceosTask} {
		force(t, b, n, api.ColumnInProgress, "")
		if err := b.SetFailed(ctx, int64(n), api.FailedAgent, "stalled"); err != nil {
			t.Fatal(err)
		}
	}

	refused := []struct {
		a Actor
		n int
	}{
		{dev, task}, // developers are never supervisors
		{agent(t, b, api.RoleReviewer, int64(task)), task},
		{pm, s},          // a pm retries tasks of its story, not the story
		{ceo, usersTask}, // supervised by the user
		{System, task},
	}
	for _, r := range refused {
		_, err := b.Retry(ctx, r.a, int64(r.n))
		wantCode(t, err, api.CodeForbidden)
	}
	other := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "O", Projects: []string{"api"}}).Number
	_, err := b.Retry(ctx, agent(t, b, api.RolePM, int64(other)), int64(task))
	wantCode(t, err, api.CodeForbidden)

	last := lastEvent(t, b)
	got, err := b.Retry(ctx, pm, int64(task))
	if err != nil {
		t.Fatal(err)
	}
	if got.FailedStep != "" || got.Attention != "" || got.Column != api.ColumnInProgress || got.CreatedBy.Session != "" {
		t.Fatalf("got failed %q attention %q column %s created_by %+v", got.FailedStep, got.Attention, got.Column, got.CreatedBy)
	}
	evs := eventsAfter(t, b, last)
	if len(evs) != 2 || evs[0].Kind != api.KindTicketUpdated || evs[1].Kind != api.KindTicketRetried {
		t.Fatalf("events %+v", evs)
	}
	if f := updatedFields(t, evs); fmt.Sprint(f) != fmt.Sprintf("[#%d failed_step,attention]", task) {
		t.Fatalf("updates %v", f)
	}
	var retried api.TicketRetriedData
	if err := json.Unmarshal([]byte(evs[1].Detail), &retried); err != nil {
		t.Fatal(err)
	}
	if retried != (api.TicketRetriedData{Ticket: task, Step: api.FailedAgent}) {
		t.Fatalf("retried %+v", retried)
	}

	_, err = b.Retry(ctx, User, int64(task))
	wantCode(t, err, api.CodeInvalidTransition)
	if !strings.Contains(err.Error(), fmt.Sprintf("nothing to retry on #%d", task)) {
		t.Fatalf("error %v", err)
	}
	if _, err := b.Retry(ctx, ceo, int64(ceosTask)); err != nil {
		t.Fatalf("ceo on its ticket: %v", err)
	}
	if _, err := b.Retry(ctx, User, int64(usersTask)); err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := b.Retry(ctx, User, int64(s)); err != nil {
		t.Fatalf("user on the story: %v", err)
	}

	// With the CEO off, its tickets are the user's.
	if err := b.SetFailed(ctx, int64(ceosTask), api.FailedAgent, "stalled again"); err != nil {
		t.Fatal(err)
	}
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	_, err = b.Retry(ctx, ceo, int64(ceosTask))
	wantCode(t, err, api.CodeForbidden)
}

func TestSetStartBlockedEmitsOnlyChanges(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	n := standaloneTask(t, b)
	planned := standaloneTask(t, b)
	if _, err := b.Approve(ctx, User, int64(n), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	last := lastEvent(t, b)
	if err := b.SetStartBlocked(ctx, map[int64]api.StartBlocked{int64(n): api.StartBlockedSlot, int64(planned): api.StartBlockedSlot, 999: api.StartBlockedSlot}); err != nil {
		t.Fatal(err)
	}
	if f := updatedFields(t, eventsAfter(t, b, last)); fmt.Sprint(f) != fmt.Sprintf("[#%d start_blocked]", n) {
		t.Fatalf("updates %v", f)
	}
	if got := get(t, b, n); got.StartBlocked != api.StartBlockedSlot {
		t.Fatalf("start_blocked %q", got.StartBlocked)
	}
	if got := get(t, b, planned); got.StartBlocked != "" {
		t.Fatalf("planning ticket got start_blocked %q", got.StartBlocked)
	}

	last, notified := lastEvent(t, b), env.notifications()
	if err := b.SetStartBlocked(ctx, map[int64]api.StartBlocked{int64(n): api.StartBlockedSlot}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetStartBlocked(ctx, map[int64]api.StartBlocked{}); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, b, last); len(evs) != 0 || env.notifications() != notified {
		t.Fatalf("unchanged: events %+v, notifications %d -> %d", evs, notified, env.notifications())
	}
	err := b.SetStartBlocked(ctx, map[int64]api.StartBlocked{int64(n): "later"})
	wantCode(t, err, api.CodeValidation)

	// Leaving Ready clears it.
	if err := b.BeginWork(ctx, int64(n)); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, n); got.StartBlocked != "" {
		t.Fatalf("in progress with start_blocked %q", got.StartBlocked)
	}

	// A claimed or failed ticket is not startable and keeps "".
	busy, failed := standaloneTask(t, b), standaloneTask(t, b)
	force(t, b, busy, api.ColumnReady, "")
	force(t, b, failed, api.ColumnReady, "")
	if err := b.ClaimStart(ctx, int64(busy)); err != nil {
		t.Fatal(err)
	}
	if err := b.SetFailed(ctx, int64(failed), api.FailedStart, "no folder"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetStartBlocked(ctx, map[int64]api.StartBlocked{int64(busy): api.StartBlockedPaused, int64(failed): api.StartBlockedPaused}); err != nil {
		t.Fatal(err)
	}
	if get(t, b, busy).StartBlocked != "" || get(t, b, failed).StartBlocked != "" {
		t.Fatal("start_blocked set on a ticket that cannot start")
	}

	// Clearing goes through, also back to "" from a value.
	w := standaloneTask(t, b)
	force(t, b, w, api.ColumnReady, "")
	for _, v := range []api.StartBlocked{api.StartBlockedNoModel, api.StartBlockedNone} {
		if err := b.SetStartBlocked(ctx, map[int64]api.StartBlocked{int64(w): v}); err != nil {
			t.Fatal(err)
		}
		if got := get(t, b, w).StartBlocked; got != v {
			t.Fatalf("start_blocked %q, want %q", got, v)
		}
	}
}

func TestCancelDiscardUncommittedRules(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}}).Number
	pm := agent(t, b, api.RolePM, int64(s))
	pmTask := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "P", Projects: []string{"api"}, Story: intPtr(s)}).Number
	_, err := b.Cancel(ctx, pm, int64(pmTask), api.CancelRequest{DeleteBranch: true, DiscardUncommitted: true})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Cancel(ctx, User, int64(pmTask), api.CancelRequest{DiscardUncommitted: true})
	wantCode(t, err, api.CodeValidation)
	if got := get(t, b, pmTask); got.Column == api.ColumnDone {
		t.Fatal("refused cancel closed the ticket")
	}

	// A second open task: the story's cancel moves the story and both tasks.
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "O", Projects: []string{"api"}, Story: intPtr(s)})
	last := lastEvent(t, b)
	if _, err := b.Cancel(ctx, User, int64(s), api.CancelRequest{DeleteBranch: true, DiscardUncommitted: true}); err != nil {
		t.Fatal(err)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 3 {
		t.Fatalf("moved %+v", moved)
	}
	for _, m := range moved {
		if m.Action != ActionCancel || m.DeleteBranch == nil || !*m.DeleteBranch || m.DiscardUncommitted == nil || !*m.DiscardUncommitted {
			t.Fatalf("event %+v", m)
		}
	}

	// A plain cancel records both as false.
	n := standaloneTask(t, b)
	last = lastEvent(t, b)
	if _, err := b.Cancel(ctx, User, int64(n), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	moved = movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 1 || moved[0].DeleteBranch == nil || *moved[0].DeleteBranch || moved[0].DiscardUncommitted == nil || *moved[0].DiscardUncommitted {
		t.Fatalf("moved %+v", moved)
	}
}

// TestUserActionsOnBusyAndFailedTickets runs every user action on tickets
// that omo has claimed or that have a failed step. The expected outcomes
// are written out by hand: "ok" or the error code.
func TestUserActionsOnBusyAndFailedTickets(t *testing.T) {
	type situation struct {
		name  string
		build func(t *testing.T, b *Board) int
	}
	ctx := context.Background()
	situations := []situation{
		{"ready task claimed for start", func(t *testing.T, b *Board) int {
			n := standaloneTask(t, b)
			force(t, b, n, api.ColumnReady, "")
			if err := b.ClaimStart(ctx, int64(n)); err != nil {
				t.Fatal(err)
			}
			return n
		}},
		{"task claimed for merge", func(t *testing.T, b *Board) int {
			n := standaloneTask(t, b)
			force(t, b, n, api.ColumnReview, api.ReviewStageMerging)
			if err := b.ClaimMerge(ctx, int64(n)); err != nil {
				t.Fatal(err)
			}
			return n
		}},
		{"story in progress, a task claimed for start", func(t *testing.T, b *Board) int {
			s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}}).Number
			force(t, b, s, api.ColumnInProgress, "")
			task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s)}).Number
			force(t, b, task, api.ColumnReady, "")
			if err := b.ClaimStart(ctx, int64(task)); err != nil {
				t.Fatal(err)
			}
			return s
		}},
		{"task in progress, failed agent", func(t *testing.T, b *Board) int {
			n := standaloneTask(t, b)
			force(t, b, n, api.ColumnInProgress, "")
			if err := b.SetFailed(ctx, int64(n), api.FailedAgent, "stalled"); err != nil {
				t.Fatal(err)
			}
			return n
		}},
		{"ready task, failed start", func(t *testing.T, b *Board) int {
			n := standaloneTask(t, b)
			force(t, b, n, api.ColumnReady, "")
			if err := b.SetFailed(ctx, int64(n), api.FailedStart, "folder exists"); err != nil {
				t.Fatal(err)
			}
			return n
		}},
		{"task merging, failed merge", func(t *testing.T, b *Board) int {
			n := standaloneTask(t, b)
			force(t, b, n, api.ColumnReview, api.ReviewStageMerging)
			if err := b.SetFailed(ctx, int64(n), api.FailedMerge, "a.go has local changes"); err != nil {
				t.Fatal(err)
			}
			return n
		}},
	}
	type action struct {
		name string
		do   func(b *Board, n int, col api.Column) error
	}
	move := func(to api.Column) func(b *Board, n int, _ api.Column) error {
		return func(b *Board, n int, _ api.Column) error {
			_, err := b.Move(ctx, User, int64(n), api.MoveRequest{Column: to})
			return err
		}
	}
	actions := []action{
		{"reorder", func(b *Board, n int, col api.Column) error {
			_, err := b.Move(ctx, User, int64(n), api.MoveRequest{Column: col})
			return err
		}},
		{"to planning", move(api.ColumnPlanning)},
		{"to ready", move(api.ColumnReady)},
		{"to done", move(api.ColumnDone)},
		{"reject", func(b *Board, n int, _ api.Column) error {
			_, err := b.Reject(ctx, User, int64(n), api.RejectRequest{Comment: "fix it"})
			return err
		}},
		{"approve", func(b *Board, n int, _ api.Column) error {
			_, err := b.Approve(ctx, User, int64(n), api.ApproveRequest{})
			return err
		}},
		{"cancel", func(b *Board, n int, _ api.Column) error {
			_, err := b.Cancel(ctx, User, int64(n), api.CancelRequest{})
			return err
		}},
		{"retry", func(b *Board, n int, _ api.Column) error {
			_, err := b.Retry(ctx, User, int64(n))
			return err
		}},
	}
	const (
		ok       = "ok"
		conflict = api.CodeConflict
		invalid  = api.CodeInvalidTransition
	)
	want := map[string]map[string]string{
		"ready task claimed for start": {
			"reorder": ok, "to planning": conflict, "to ready": ok, "to done": invalid,
			"reject": conflict, "approve": invalid, "cancel": conflict, "retry": invalid,
		},
		"task claimed for merge": {
			"reorder": ok, "to planning": invalid, "to ready": invalid, "to done": invalid,
			"reject": conflict, "approve": invalid, "cancel": conflict, "retry": invalid,
		},
		"story in progress, a task claimed for start": {
			"reorder": ok, "to planning": conflict, "to ready": conflict, "to done": invalid,
			"reject": conflict, "approve": invalid, "cancel": conflict, "retry": invalid,
		},
		"task in progress, failed agent": {
			"reorder": ok, "to planning": ok, "to ready": ok, "to done": invalid,
			"reject": invalid, "approve": invalid, "cancel": ok, "retry": ok,
		},
		"ready task, failed start": {
			"reorder": ok, "to planning": ok, "to ready": ok, "to done": invalid,
			"reject": invalid, "approve": invalid, "cancel": ok, "retry": ok,
		},
		"task merging, failed merge": {
			"reorder": ok, "to planning": invalid, "to ready": invalid, "to done": invalid,
			"reject": invalid, "approve": invalid, "cancel": ok, "retry": ok,
		},
	}
	for _, s := range situations {
		for _, a := range actions {
			t.Run(s.name+"/"+a.name, func(t *testing.T) {
				b, _ := newTestBoard(t)
				n := s.build(t, b)
				before := get(t, b, n)
				err := a.do(b, n, before.Column)
				expect := want[s.name][a.name]
				after := get(t, b, n)
				if expect != ok {
					wantCode(t, err, expect)
					if after.Column != before.Column || after.ReviewStage != before.ReviewStage ||
						after.Busy != before.Busy || after.FailedStep != before.FailedStep {
						t.Fatalf("refused action changed the ticket: %+v", after.TicketSummary)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if after.Column != before.Column && (after.FailedStep != "" || after.Busy) {
					t.Fatalf("column change kept failed %q busy %v", after.FailedStep, after.Busy)
				}
				if a.name == "retry" && (after.FailedStep != "" || after.Column != before.Column) {
					t.Fatalf("retry left %q in %s", after.FailedStep, after.Column)
				}
			})
		}
	}
}
