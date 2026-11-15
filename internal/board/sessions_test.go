package board

import (
	"context"
	"encoding/json"
	"path/filepath"
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
	if len(got.Agents) != 1 || got.Agents[0] != (api.AgentRef{Session: s.ID, Role: api.RoleDeveloper, Name: "Ada", State: api.SessionStarting, Project: "api"}) {
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
	all, err := b.Sessions(ctx, n, false)
	if err != nil || len(all) != 1 || all[0].State != api.SessionEnded || all[0].EndReason != "done" || all[0].EndedAt == nil {
		t.Fatalf("Sessions = %+v, %v", all, err)
	}
	live, err := b.Sessions(ctx, n, true)
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
	all, err := b.Sessions(ctx, 0, true)
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
