package board

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestSessionLifecycleAndResolve(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	n := int64(task.Number)

	s, err := b.CreateSession(ctx, NewSession{Role: api.RoleDeveloper, Ticket: n, Project: "api", Name: "Ada", Model: "claude/fable", Workdir: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ID) != 32 || s.State != api.SessionStarting || s.Ticket == nil || *s.Ticket != task.Number {
		t.Fatalf("session = %+v", s)
	}
	a, err := b.ResolveSession(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Role != api.RoleDeveloper || a.Ticket != n || a.Session != s.ID || a.Project != "api" || a.Name != "Ada" {
		t.Fatalf("actor = %+v", a)
	}
	got, err := b.Get(ctx, User, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Agents) != 1 || got.Agents[0] != (api.AgentRef{Session: s.ID, Role: api.RoleDeveloper, Name: "Ada", State: api.SessionStarting, Project: "api", Purpose: api.PurposeWork}) {
		t.Fatalf("Agents = %+v", got.Agents)
	}

	if err := b.SetSessionState(ctx, s.ID, api.SessionWaiting); err != nil {
		t.Fatal(err)
	}
	if err := b.SetSessionState(ctx, s.ID, api.SessionEnded); errCode(err) != api.CodeValidation {
		t.Fatalf("SetSessionState ended = %v", err)
	}
	if err := b.EndSession(ctx, s.ID, "done"); err != nil {
		t.Fatal(err)
	}
	_, err = b.ResolveSession(ctx, s.ID)
	wantCode(t, err, api.CodeUnknownSession)
	_, err = b.ResolveSession(ctx, "deadbeef")
	wantCode(t, err, api.CodeUnknownSession)
	if err := b.SetSessionState(ctx, s.ID, api.SessionRunning); errCode(err) != api.CodeConflict {
		t.Fatalf("SetSessionState after end = %v", err)
	}
	if err := b.EndSession(ctx, "deadbeef", "x"); errCode(err) != api.CodeNotFound {
		t.Fatalf("EndSession unknown = %v", err)
	}

	got, err = b.Get(ctx, User, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Agents) != 0 {
		t.Fatalf("Agents after end = %+v", got.Agents)
	}
	all, err := sessions(b.db, n, false)
	if err != nil || len(all) != 1 || all[0].State != api.SessionEnded || all[0].EndReason != "done" || all[0].EndedAt == nil {
		t.Fatalf("Sessions = %+v, %v", all, err)
	}
	live, err := sessions(b.db, n, true)
	if err != nil || len(live) != 0 {
		t.Fatalf("live Sessions = %+v, %v", live, err)
	}

	evs, err := db.EventsSince(b.db, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	want := []string{api.KindTicketCreated, api.KindAgentCreated, api.KindTicketUpdated, api.KindAgentUpdated, api.KindTicketUpdated, api.KindAgentEnded, api.KindTicketUpdated}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	var data api.AgentData
	if err := json.Unmarshal([]byte(evs[5].Detail), &data); err != nil || data.Agent.State != api.SessionEnded {
		t.Fatalf("agent.ended data = %s, %v", evs[5].Detail, err)
	}
}

func TestSessionRoleBinding(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	bad := []NewSession{
		{Role: api.RoleCEO, Ticket: int64(story.Number)},
		{Role: api.RolePM, Ticket: int64(task.Number)},
		{Role: api.RolePM},
		{Role: api.RoleDeveloper, Ticket: int64(story.Number)},
		{Role: api.RoleReviewer, Ticket: int64(story.Number), Project: "ui"},
		{Role: api.RoleUser},
		{Role: api.RoleDeveloper, Ticket: 999},
	}
	for _, s := range bad {
		if _, err := b.CreateSession(ctx, s); errCode(err) != api.CodeValidation {
			t.Errorf("CreateSession(%+v) = %v, want validation", s, err)
		}
	}
	ceo, err := b.CreateSession(ctx, NewSession{Role: api.RoleCEO, Name: "Boss", Model: "claude/fable"})
	if err != nil || ceo.Ticket != nil {
		t.Fatalf("ceo session = %+v, %v", ceo, err)
	}
	all, err := b.AgentSessions(ctx, User, true)
	if err != nil || len(all) != 1 {
		t.Fatalf("all live sessions = %+v, %v", all, err)
	}
}

func TestPausedSurvivesReopen(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "omo.db")
	env := newFakeEnv()

	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b := New(d, env, testOptions(env))
	if p, err := b.Paused(ctx); err != nil || p {
		t.Fatalf("initial Paused = %v, %v", p, err)
	}
	if err := b.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	evs, err := db.EventsSince(d, 0, 0, true)
	if err != nil || len(evs) != 1 || evs[0].Kind != api.KindServerPaused {
		t.Fatalf("events = %+v, %v", evs, err)
	}
	d.Close()

	d2 := openTestDB(t, path)
	b2 := New(d2, env, testOptions(env))
	if p, err := b2.Paused(ctx); err != nil || !p {
		t.Fatalf("Paused after reopen = %v, %v", p, err)
	}
	if err := b2.SetPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	if p, _ := b2.Paused(ctx); p {
		t.Fatal("still paused")
	}
}

func TestAgentSessionsScope(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}}).Number
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story)}).Number
	ceo := agent(t, b, api.RoleCEO, 0)
	pm := agent(t, b, api.RolePM, int64(story))
	dev := agent(t, b, api.RoleDeveloper, int64(task))
	reviewer := agent(t, b, api.RoleReviewer, int64(task))
	if err := b.EndSession(ctx, reviewer.Session, "test"); err != nil {
		t.Fatal(err)
	}
	reviewer = agent(t, b, api.RoleReviewer, int64(story))
	for _, a := range []Actor{User, ceo, pm} {
		all, err := b.AgentSessions(ctx, a, false)
		if err != nil || len(all) != 5 {
			t.Fatalf("%s lists %d sessions, %v", a.Role, len(all), err)
		}
		live, err := b.AgentSessions(ctx, a, true)
		if err != nil || len(live) != 4 {
			t.Fatalf("%s lists %d live sessions, %v", a.Role, len(live), err)
		}
	}
	for _, a := range []Actor{dev, reviewer} {
		_, err := b.AgentSessions(ctx, a, false)
		wantCode(t, err, api.CodeForbidden)
	}
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	_, err := b.AgentSessions(ctx, ceo, false)
	wantCode(t, err, api.CodeForbidden)
}

func TestExportSessionBinding(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}})
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	s, n := int64(story.Number), int64(task.Number)
	force(t, b, task.Number, api.ColumnReview, api.ReviewStageHuman)
	force(t, b, story.Number, api.ColumnReview, api.ReviewStageAgent)

	bad := []NewSession{
		{Role: api.RoleDeveloper, Ticket: n, Project: "api", Purpose: api.PurposeExport}, // not exporting
		{Role: api.RoleReviewer, Ticket: s, Project: "api", ReviewRound: 0},              // stale round
		{Role: api.RoleReviewer, Ticket: s, Project: "api", ReviewRound: 2},              // future round
		{Role: api.RoleDeveloper, Ticket: n, Project: "api", Purpose: "deploy"},          // unknown purpose
		{Role: api.RolePM, Ticket: s, Project: "api", Purpose: api.PurposeExport},        // export is a developer job
		{Role: api.RoleReviewer, Ticket: s, Project: "api", Purpose: api.PurposeExport},  // same
		{Role: api.RoleDeveloper, Ticket: n, Project: "api", ReviewRound: 1},             // only reviewers have a round
	}
	for _, ns := range bad {
		if _, err := b.CreateSession(ctx, ns); errCode(err) != api.CodeValidation {
			t.Errorf("CreateSession(%+v) = %v, want validation", ns, err)
		}
	}
	rev, err := b.CreateSession(ctx, NewSession{Role: api.RoleReviewer, Ticket: s, Project: "api", ReviewRound: 1})
	if err != nil || rev.ReviewRound != 1 || rev.Purpose != api.PurposeWork {
		t.Fatalf("reviewer = %+v, %v", rev, err)
	}

	force(t, b, task.Number, api.ColumnReview, api.ReviewStageExporting)
	force(t, b, story.Number, api.ColumnReview, api.ReviewStageExporting)
	for _, ns := range []NewSession{
		{Role: api.RoleDeveloper, Ticket: n, Purpose: api.PurposeExport},                 // project required
		{Role: api.RoleDeveloper, Ticket: s, Project: "off", Purpose: api.PurposeExport}, // not a project of the story
	} {
		if _, err := b.CreateSession(ctx, ns); errCode(err) != api.CodeValidation {
			t.Errorf("CreateSession(%+v) = %v, want validation", ns, err)
		}
	}
	// An export job runs for a standalone task and for each project of a
	// story, as a developer session.
	for _, ns := range []NewSession{
		{Role: api.RoleDeveloper, Ticket: n, Project: "api", Purpose: api.PurposeExport},
		{Role: api.RoleDeveloper, Ticket: s, Project: "ui", Purpose: api.PurposeExport},
	} {
		got, err := b.CreateSession(ctx, ns)
		if err != nil || got.Purpose != api.PurposeExport || got.ReviewRound != 0 {
			t.Fatalf("CreateSession(%+v) = %+v, %v", ns, got, err)
		}
		a, err := b.ResolveSession(ctx, got.ID)
		if err != nil || a.Purpose != api.PurposeExport || a.Project != ns.Project {
			t.Fatalf("actor = %+v, %v", a, err)
		}
	}
	if got := get(t, b, story.Number); len(got.Agents) != 2 || got.Agents[1].Purpose != api.PurposeExport {
		t.Fatalf("agents = %+v", got.Agents)
	}
}

func TestMentionOfDeveloperSkipsExportSession(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	n := int64(tk.Number)

	// Only an export job is live: nobody works on the task.
	job := exportJob(t, b, tk.Number, "api")
	res, err := b.Comment(ctx, User, n, api.CommentRequest{Body: "@developer how is it going?"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Comment.Mentions) != 1 || res.Comment.Mentions[0] != (api.Mention{Role: api.RoleDeveloper}) ||
		len(res.Unreachable) != 1 || res.Unreachable[0] != api.RoleDeveloper {
		t.Fatalf("mentions %+v unreachable %+v", res.Comment.Mentions, res.Unreachable)
	}

	// With the task's developer live (started after the job), the mention
	// reaches the developer, not the newer export job.
	dev, err := b.CreateSession(ctx, NewSession{Role: api.RoleDeveloper, Ticket: n, Project: "api", Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.EndSession(ctx, job.Session, "export over"); err != nil {
		t.Fatal(err)
	}
	job2 := exportJob(t, b, tk.Number, "api")
	res, err = b.Comment(ctx, User, n, api.CommentRequest{Body: "@developer and now?"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Comment.Mentions) != 1 || res.Comment.Mentions[0] != (api.Mention{Role: api.RoleDeveloper, Session: dev.ID}) ||
		res.Comment.Mentions[0].Session == job.Session || res.Comment.Mentions[0].Session == job2.Session {
		t.Fatalf("mentions %+v", res.Comment.Mentions)
	}
}

func TestPreviousSessionReturnsNewestEnded(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	n := int64(tk.Number)

	if _, ok, err := b.PreviousSession(ctx, n, api.RoleDeveloper, "api", ""); err != nil || ok {
		t.Fatalf("none yet: %v, %v", ok, err)
	}
	start := func(model string) api.AgentSession {
		t.Helper()
		s, err := b.CreateSession(ctx, NewSession{Role: api.RoleDeveloper, Ticket: n, Project: "api", Name: "dev", Model: model})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	// One slot has at most one live session, so each ends before the next
	// starts.
	first := start("claude/opus")
	if err := b.EndSession(ctx, first.ID, "crashed"); err != nil {
		t.Fatal(err)
	}
	second := start("claude/fable")
	if err := b.EndSession(ctx, second.ID, "crashed"); err != nil {
		t.Fatal(err)
	}
	live := start("claude/fable")
	// The newest ended session wins; the live session is not a previous one.
	got, ok, err := b.PreviousSession(ctx, n, api.RoleDeveloper, "api", api.PurposeWork)
	if err != nil || !ok || got.ID != second.ID || got.Model != "claude/fable" || got.State != api.SessionEnded || got.EndReason != "crashed" {
		t.Fatalf("previous = %+v, %v, %v", got, ok, err)
	}
	if got.ID == live.ID {
		t.Fatal("live session returned")
	}
	// Other slots do not count.
	for _, slot := range []struct {
		role    api.Role
		project string
		purpose api.Purpose
	}{
		{api.RoleReviewer, "api", ""},
		{api.RoleDeveloper, "ui", ""},
		{api.RoleDeveloper, "api", api.PurposeExport},
	} {
		if _, ok, err := b.PreviousSession(ctx, n, slot.role, slot.project, slot.purpose); err != nil || ok {
			t.Fatalf("slot %+v: %v, %v", slot, ok, err)
		}
	}
}

func TestCreateSessionRefusesSecondLiveSessionOfSlot(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}, WhenApproved: api.WhenApprovedExport}).Number
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}}).Number
	create := func(ns NewSession) (api.AgentSession, error) {
		ns.Name, ns.Model = "agent", "claude/fable"
		return b.CreateSession(ctx, ns)
	}
	mustStart := func(ns NewSession) api.AgentSession {
		t.Helper()
		got, err := create(ns)
		if err != nil {
			t.Fatalf("CreateSession(%+v) = %v", ns, err)
		}
		return got
	}
	refused := func(ns NewSession, slot string) {
		t.Helper()
		_, err := create(ns)
		wantCode(t, err, api.CodeConflict)
		if !strings.Contains(err.Error(), slot) {
			t.Fatalf("error %v does not name %q", err, slot)
		}
	}

	// A developer working on the task, parked in a wait.
	dev := mustStart(NewSession{Role: api.RoleDeveloper, Ticket: int64(tk), Project: "api"})
	if err := b.SetSessionState(ctx, dev.ID, api.SessionWaiting); err != nil {
		t.Fatal(err)
	}
	refused(NewSession{Role: api.RoleDeveloper, Ticket: int64(tk), Project: "api"}, `developer session on #`+itoa(tk)+` for project "api"`)
	// Its export job is another purpose and may run beside it.
	force(t, b, tk, api.ColumnReview, api.ReviewStageExporting)
	job := mustStart(NewSession{Role: api.RoleDeveloper, Ticket: int64(tk), Project: "api", Purpose: api.PurposeExport})
	refused(NewSession{Role: api.RoleDeveloper, Ticket: int64(tk), Project: "api", Purpose: api.PurposeExport}, "export job on #"+itoa(tk))
	// Once the old session ended, the slot is free again.
	if err := b.EndSession(ctx, job.ID, "export over"); err != nil {
		t.Fatal(err)
	}
	mustStart(NewSession{Role: api.RoleDeveloper, Ticket: int64(tk), Project: "api", Purpose: api.PurposeExport})

	// Reviewers: one per project and round.
	force(t, b, s, api.ColumnReview, api.ReviewStageAgent)
	mustStart(NewSession{Role: api.RoleReviewer, Ticket: int64(s), Project: "api", ReviewRound: 1})
	mustStart(NewSession{Role: api.RoleReviewer, Ticket: int64(s), Project: "ui", ReviewRound: 1})
	refused(NewSession{Role: api.RoleReviewer, Ticket: int64(s), Project: "api", ReviewRound: 1}, "in review round 1")
	if _, err := b.db.Exec(`UPDATE tickets SET review_round = 2 WHERE id = ?`, s); err != nil {
		t.Fatal(err)
	}
	mustStart(NewSession{Role: api.RoleReviewer, Ticket: int64(s), Project: "api", ReviewRound: 2})

	// The pm of the story and the CEO.
	mustStart(NewSession{Role: api.RolePM, Ticket: int64(s), Project: "api"})
	refused(NewSession{Role: api.RolePM, Ticket: int64(s), Project: "api"}, "pm session on #"+itoa(s))
	mustStart(NewSession{Role: api.RoleCEO})
	refused(NewSession{Role: api.RoleCEO}, "live ceo session exists")
}
