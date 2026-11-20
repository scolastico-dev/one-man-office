package board

import (
	"context"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// storyWithBranches creates a story by a on api, ui and web, records a
// branch and worktree in each and puts it in Review waiting for the user.
func storyWithBranches(t *testing.T, b *Board, a Actor) int {
	t.Helper()
	ctx := context.Background()
	s := mustCreate(t, b, a, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui", "web"}})
	for _, p := range []string{"api", "ui", "web"} {
		if err := b.SetBranch(ctx, int64(s.Number), p, "omo/1-s", "/home/omo/worktrees/"+p+"/1"); err != nil {
			t.Fatal(err)
		}
	}
	force(t, b, s.Number, api.ColumnReview, api.ReviewStageHuman)
	return s.Number
}

func TestRemoveProjectRecordsWhoRemovedWhat(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := storyWithBranches(t, b, User)
	// A verdict per project from the last round, and a failed export of ui
	// and web that the attention flag names.
	for _, p := range []string{"api", "ui", "web"} {
		if _, err := b.db.Exec(`INSERT INTO review_verdicts (ticket_id, round, project_id, verdict, created_at) VALUES (?, 1, ?, 'approve', ?)`,
			n, p, b.stamp()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.db.Exec(`UPDATE ticket_projects SET export_state = 'failed', export_error = 'no remote' WHERE ticket_id = ? AND project_id IN ('ui', 'web')`, n); err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.Exec(`UPDATE tickets SET attention_owner = 'export', attention = 'Export of ui failed: no remote; Export of web failed: no remote' WHERE id = ?`, n); err != nil {
		t.Fatal(err)
	}

	r, err := b.ClaimProjectRemoval(ctx, User, int64(n), "web")
	if err != nil {
		t.Fatal(err)
	}
	if r.Project.ID != "web" || r.Project.Branch != "omo/1-s" || r.Ticket.ID != int64(n) || len(r.Parked) != 0 {
		t.Fatalf("removal = %+v", r)
	}
	if !get(t, b, n).Busy {
		t.Fatal("the claim did not mark the ticket busy")
	}
	// While the engine works on it, nothing else claims the ticket.
	_, err = b.ClaimProjectRemoval(ctx, User, int64(n), "ui")
	wantCode(t, err, api.CodeConflict)

	last := lastEvent(t, b)
	got, err := b.RemoveProject(ctx, User, int64(n), "web", "omo removed its worktree.")
	if err != nil {
		t.Fatal(err)
	}
	if ids := projectIDs(got); strings.Join(ids, ",") != "api,ui" || got.Busy {
		t.Fatalf("projects %v busy %v", ids, got.Busy)
	}
	if got.Attention != "Export of ui failed: no remote" {
		t.Fatalf("attention %q", got.Attention)
	}
	var verdicts int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM review_verdicts WHERE ticket_id = ? AND project_id = 'web'`, n).Scan(&verdicts); err != nil || verdicts != 0 {
		t.Fatalf("verdicts of web left: %d %v", verdicts, err)
	}
	c, ok := lastComment(t, b, n)
	if !ok || c.Author.Role != api.RoleSystem || c.Body != "The user removed project `web` from #"+itoa(n)+". omo removed its worktree." {
		t.Fatalf("comment = %+v", c)
	}
	evs := eventsAfter(t, b, last)
	found := false
	for _, e := range evs {
		if e.Kind == api.KindTicketUpdated && strings.Contains(e.Detail, `"fields":["projects","busy","attention"]`) &&
			strings.Contains(e.Actor, `"role":"user"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no ticket.updated by the user with projects, busy and attention: %+v", evs)
	}
	// Without a claim the engine cannot record a removal.
	_, err = b.RemoveProject(ctx, User, int64(n), "ui", "")
	wantCode(t, err, api.CodeConflict)

	// Removing the last failed project clears the flag.
	if _, err := b.ClaimProjectRemoval(ctx, User, int64(n), "ui"); err != nil {
		t.Fatal(err)
	}
	if got, err = b.RemoveProject(ctx, User, int64(n), "ui", ""); err != nil || got.Attention != "" {
		t.Fatalf("attention %q, %v", got.Attention, err)
	}
	// A ticket keeps one project.
	_, err = b.ClaimProjectRemoval(ctx, User, int64(n), "api")
	wantCode(t, err, api.CodeValidation)
	if err == nil || !strings.Contains(err.Error(), "only project") {
		t.Fatalf("error %v", err)
	}
}

func TestRemoveProjectWithTruncatedExportAttention(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "Export", Projects: []string{"api", "ui"}})
	n := int64(s.Number)
	force(t, b, s.Number, api.ColumnReview, api.ReviewStageExporting)
	reason := strings.Repeat("a", 160)
	for project, why := range map[string]string{"api": reason, "ui": "no remote"} {
		if err := b.SetProjectExport(ctx, n, project, api.ExportFailedState, why); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.ExportFailed(ctx, n, "Export of api failed: "+reason+"; Export of ui failed: no remote"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ClaimProjectRemoval(ctx, User, n, "ui"); err != nil {
		t.Fatal(err)
	}
	got, err := b.RemoveProject(ctx, User, n, "ui", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 || got.Projects[0].Project != "api" {
		t.Fatalf("remaining projects: %+v", got.Projects)
	}
	if want := "Export of api failed: " + reason; got.Attention != want {
		t.Fatalf("attention = %q, want %q", got.Attention, want)
	}
}

func TestLegacyTruncatedExportAttentionCanBeCleared(t *testing.T) {
	for _, action := range []string{"retry export", "reject to In Progress"} {
		t.Run(action, func(t *testing.T) {
			b, env := newTestBoard(t)
			ctx := context.Background()
			s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "Export", Projects: []string{"api", "ui"}})
			n := int64(s.Number)
			force(t, b, s.Number, api.ColumnReview, api.ReviewStageExporting)
			reason := strings.Repeat("a", 160)
			for project, why := range map[string]string{"api": reason, "ui": "no remote"} {
				if err := b.SetProjectExport(ctx, n, project, api.ExportFailedState, why); err != nil {
					t.Fatal(err)
				}
			}
			if err := b.ExportFailed(ctx, n, "Export of api failed: "+reason+"; Export of ui failed: no remote"); err != nil {
				t.Fatal(err)
			}
			before := get(t, b, s.Number).Attention
			if !strings.HasSuffix(before, "; Export of ui fa…") {
				t.Fatalf("not the legacy shortened case: %q", before)
			}
			// Remove the new metadata columns to reopen the actual version-10
			// shape through the normal upgrade path.
			var seq int
			var name, path string
			if err := b.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
				t.Fatal(err)
			}
			if _, err := b.db.Exec(`ALTER TABLE ticket_projects DROP COLUMN last_export_commit;
ALTER TABLE ticket_projects DROP COLUMN last_export_url;
ALTER TABLE ticket_projects DROP COLUMN review_commit;
ALTER TABLE tickets DROP COLUMN followup_review;
ALTER TABLE tickets DROP COLUMN reexport_pending;
ALTER TABLE tickets DROP COLUMN attention_owner;
ALTER TABLE agent_sessions DROP COLUMN end_kind; PRAGMA user_version = 10`); err != nil {
				t.Fatal(err)
			}
			b.db.Close()
			d, err := db.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			b = New(d, env, Options{})
			if _, err := b.ClaimProjectRemoval(ctx, User, n, "ui"); err != nil {
				t.Fatal(err)
			}
			got, err := b.RemoveProject(ctx, User, n, "ui", "")
			if err != nil || got.Attention != before {
				t.Fatalf("unknown legacy flag was reclassified: %q, %v", got.Attention, err)
			}
			if action == "retry export" {
				got, err = b.Approve(ctx, User, n, api.ApproveRequest{Export: true})
				if err != nil || got.ReviewStage != api.ReviewStageExporting {
					t.Fatalf("retry: %+v %v", got, err)
				}
			} else {
				got, err = b.Reject(ctx, User, n, api.RejectRequest{Comment: "Review the remaining project again"})
				if err != nil || got.Column != api.ColumnInProgress {
					t.Fatalf("move: %+v %v", got, err)
				}
			}
			if got.Attention != "" {
				t.Fatalf("legacy warning remains after %s: %q", action, got.Attention)
			}
		})
	}
}

func TestRemoveProjectWho(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	byUser := storyWithBranches(t, b, User)
	ceo := agent(t, b, api.RoleCEO, 0)
	byCEO := storyWithBranches(t, b, ceo)
	supervised := storyWithBranches(t, b, User)
	if _, err := b.db.Exec(`UPDATE tickets SET supervisor = 'ceo' WHERE id = ?`, supervised); err != nil {
		t.Fatal(err)
	}

	_, err := b.ClaimProjectRemoval(ctx, ceo, int64(byUser), "web")
	wantCode(t, err, api.CodeForbidden)
	for _, n := range []int{byCEO, supervised} {
		if _, err := b.ClaimProjectRemoval(ctx, ceo, int64(n), "web"); err != nil {
			t.Fatalf("CEO on #%d: %v", n, err)
		}
		got, err := b.RemoveProject(ctx, ceo, int64(n), "web", "")
		if err != nil {
			t.Fatal(err)
		}
		c, _ := lastComment(t, b, n)
		if !strings.HasPrefix(c.Body, "The CEO (ceo-agent) removed project `web`") || len(got.Projects) != 2 {
			t.Fatalf("#%d: %q %v", n, c.Body, projectIDs(got))
		}
	}
	for _, role := range []api.Role{api.RolePM, api.RoleReviewer} {
		a := agent(t, b, role, int64(byUser))
		_, err := b.ClaimProjectRemoval(ctx, a, int64(byUser), "web")
		wantCode(t, err, api.CodeForbidden)
	}
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	_, err = b.ClaimProjectRemoval(ctx, agent(t, b, api.RoleDeveloper, int64(task.Number)), int64(task.Number), "api")
	wantCode(t, err, api.CodeForbidden)
	// The CEO switched off is refused like any of its calls.
	b2, env := newTestBoard(t)
	ceo2 := agent(t, b2, api.RoleCEO, 0)
	n := storyWithBranches(t, b2, ceo2)
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	_, err = b2.ClaimProjectRemoval(ctx, ceo2, int64(n), "web")
	wantCode(t, err, api.CodeForbidden)
}

func TestRemoveProjectWhen(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := storyWithBranches(t, b, User)
	for _, c := range []struct {
		col   api.Column
		stage api.ReviewStage
		ok    bool
	}{
		{api.ColumnPlanning, "", true},
		{api.ColumnReady, "", true},
		{api.ColumnInProgress, "", false},
		{api.ColumnReview, api.ReviewStageAgent, false},
		{api.ColumnReview, api.ReviewStageMerging, false},
		{api.ColumnReview, api.ReviewStageExporting, false},
		{api.ColumnReview, api.ReviewStageHuman, true},
		{api.ColumnDone, "", false},
	} {
		force(t, b, n, c.col, c.stage)
		_, err := b.ClaimProjectRemoval(ctx, User, int64(n), "web")
		if c.ok {
			if err != nil {
				t.Fatalf("%s %s: %v", c.col, c.stage, err)
			}
			if err := b.ReleaseClaim(ctx, int64(n)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		wantCode(t, err, api.CodeInvalidTransition)
	}
	force(t, b, n, api.ColumnReview, api.ReviewStageHuman)
	_, err := b.ClaimProjectRemoval(ctx, User, int64(n), "nope")
	wantCode(t, err, api.CodeValidation)
	_, err = b.ClaimProjectRemoval(ctx, User, int64(n), " ")
	wantCode(t, err, api.CodeValidation)
}

func TestRemoveProjectFromStoryWithTasks(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := storyWithBranches(t, b, User)
	force(t, b, n, api.ColumnInProgress, "")
	open := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "open", Projects: []string{"web"}, Story: intPtr(n)})
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "t", Projects: []string{"api"}, Story: intPtr(n)})
	force(t, b, n, api.ColumnReview, api.ReviewStageHuman)
	_, err := b.ClaimProjectRemoval(ctx, User, int64(n), "web")
	wantCode(t, err, api.CodeValidation)
	if !strings.Contains(err.Error(), "task #"+itoa(open.Number)) {
		t.Fatalf("error %v", err)
	}
	// A done task keeps the project as its history and does not hold it.
	forceDone(t, b, open.Number, api.ResolutionCompleted)
	if _, err := b.ClaimProjectRemoval(ctx, User, int64(n), "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.RemoveProject(ctx, User, int64(n), "web", ""); err != nil {
		t.Fatal(err)
	}
	if ids := projectIDs(get(t, b, open.Number)); len(ids) != 1 || ids[0] != "web" {
		t.Fatalf("the done task's projects %v", ids)
	}
	// A task's claim holds its story too.
	if _, err := b.db.Exec(`UPDATE tickets SET busy = 1 WHERE id = ?`, task.Number); err != nil {
		t.Fatal(err)
	}
	_, err = b.ClaimProjectRemoval(ctx, User, int64(n), "ui")
	wantCode(t, err, api.CodeConflict)
}

func TestRemoveProjectAgents(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := storyWithBranches(t, b, User)
	worktree := "/home/omo/worktrees/api/1"
	// The pm works in the first project's worktree, a reviewer in ui.
	pm, err := b.CreateSession(ctx, NewSession{Role: api.RolePM, Ticket: int64(n), Name: "pm-ann", Model: "claude/fable", Workdir: worktree})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := b.CreateSession(ctx, NewSession{Role: api.RoleReviewer, Ticket: int64(n), Project: "ui", Name: "reviewer-bo", Model: "claude/fable",
		Workdir: "/home/omo/worktrees/ui/1", ReviewRound: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{pm.ID, rev.ID} {
		if err := b.SetSessionState(ctx, id, api.SessionRunning); err != nil {
			t.Fatal(err)
		}
	}
	for project, name := range map[string]string{"api": "pm-ann", "ui": "reviewer-bo"} {
		_, err := b.ClaimProjectRemoval(ctx, User, int64(n), project)
		wantCode(t, err, api.CodeConflict)
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: %v", project, err)
		}
	}
	// web has nobody.
	if _, err := b.ClaimProjectRemoval(ctx, User, int64(n), "web"); err != nil {
		t.Fatal(err)
	}
	if err := b.ReleaseClaim(ctx, int64(n)); err != nil {
		t.Fatal(err)
	}
	// Agents that only wait are handed to the engine to end.
	if err := b.SetSessionState(ctx, rev.ID, api.SessionWaiting); err != nil {
		t.Fatal(err)
	}
	r, err := b.ClaimProjectRemoval(ctx, User, int64(n), "ui")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Parked) != 1 || r.Parked[0].ID != rev.ID {
		t.Fatalf("parked %+v", r.Parked)
	}
}

func TestFieldLockedNamesRemoveProject(t *testing.T) {
	b, _ := newTestBoard(t)
	n := storyWithBranches(t, b, User)
	force(t, b, n, api.ColumnPlanning, "")
	_, err := b.Update(context.Background(), User, int64(n), api.UpdateTicket{Projects: api.Some([]string{"api", "ui"})})
	wantCode(t, err, api.CodeFieldLocked)
	if !strings.Contains(err.Error(), "omo ai ticket remove-project "+itoa(n)+" PROJECT") {
		t.Fatalf("error %v", err)
	}
}
