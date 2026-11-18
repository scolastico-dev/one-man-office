package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/engine"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

func TestPluginAuthoritativeFolderContext(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	ctx := context.Background()
	p, err := s.settings.AddProject(committedRepo(t, "main"), "first")
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.settings.AddProject(committedRepo(t, "main"), "second")
	if err != nil {
		t.Fatal(err)
	}
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeStory, Title: "folders", Projects: []string{p.ID, q.ID}})
	if err != nil {
		t.Fatal(err)
	}
	secondPath := ""
	check := func(want string, available bool) {
		t.Helper()
		current, err := s.board.Get(ctx, board.User, int64(tk.Number))
		if err != nil {
			t.Fatal(err)
		}
		payload := s.pluginTicketPayload(ctx, current.TicketSummary)
		ids := payload["projects"].([]any)
		folders := payload["folders"].([]any)
		if len(ids) != 2 || ids[0] != p.ID || ids[1] != q.ID || len(folders) != 2 {
			t.Fatalf("payload: %+v", payload)
		}
		f := folders[0].(map[string]any)
		if f["path"] != want || f["available"] != available || f["project"] != p.ID {
			t.Fatalf("folder: %+v", f)
		}
		second := folders[1].(map[string]any)
		if second["available"] != (secondPath != "") || second["path"] != secondPath || (secondPath == "" && second["reason"] == "") {
			t.Fatalf("unavailable: %+v", second)
		}
	}
	check("", false)
	wt := engine.WorktreeDir(s.opts.Layout.Worktrees, p.ID, int64(tk.Number))
	gitIn(t, p.Path, "worktree", "add", "-q", "-b", "plugin-folders", wt)
	if err := s.board.SetBranch(ctx, int64(tk.Number), p.ID, "plugin-folders", wt); err != nil {
		t.Fatal(err)
	}
	check(pluginRealPath(t, wt), true)
	qwt := engine.WorktreeDir(s.opts.Layout.Worktrees, q.ID, int64(tk.Number))
	gitIn(t, q.Path, "worktree", "add", "-q", "-b", "other-plugin-folder", qwt)
	if err := s.board.SetBranch(ctx, int64(tk.Number), q.ID, "other-plugin-folder", qwt); err != nil {
		t.Fatal(err)
	}
	secondPath = pluginRealPath(t, qwt)
	check(pluginRealPath(t, wt), true)
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /untrusted\n"), 0600); err != nil {
		t.Fatal(err)
	}
	check("", false)
	// Project selections use the repository and never any browser path.
	pr, err := s.pluginProjectContext(ctx, 0, p.ID, nil)
	if err != nil || pr["path"] != pluginRealPath(t, p.Path) || pr["kind"] != "repository" {
		t.Fatalf("project: %+v %v", pr, err)
	}
	_, err = s.pluginProjectContext(ctx, tk.Number, q.ID, []api.TicketProject{{Project: p.ID}})
	if err == nil {
		t.Fatal("mismatched project accepted")
	}
	if e, ok := err.(*api.Error); !ok || e.Details != (api.FieldDetail{Field: "project"}) {
		t.Fatalf("project field detail lost: %+v", err)
	}
	_, err = s.pluginProjectContext(ctx, tk.Number, p.ID, tk.Projects)
	if err == nil {
		t.Fatal("untrusted worktree accepted")
	}
	if host := plugins.HostPayload(); host["os"] != runtime.GOOS || host["arch"] != runtime.GOARCH {
		t.Fatalf("host: %+v", host)
	}
}

func TestPluginFolderAvailabilityAndEventPropagation(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	ctx := t.Context()
	p, err := s.settings.AddProject(committedRepo(t, "main"), "app")
	if err != nil {
		t.Fatal(err)
	}
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "folders", Projects: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.board.Get(ctx, board.User, int64(tk.Number))
	if err != nil {
		t.Fatal(err)
	}
	payload := s.pluginTicketPayload(ctx, current.TicketSummary)
	lookup := func(n int) map[string]any { return payload }
	evs := []api.Event{
		boardEvent(t, 1, api.KindTicketCreated, api.TicketCreatedData{Ticket: current.TicketSummary}),
		boardEvent(t, 2, api.KindTicketMoved, api.TicketMovedData{Ticket: current.TicketSummary, From: api.ColumnPlanning, To: api.ColumnReady}),
		boardEvent(t, 3, api.KindCommentCreated, api.CommentCreatedData{Comment: api.Comment{Ticket: tk.Number}}),
		boardEvent(t, 4, api.KindAgentCreated, api.AgentData{Agent: api.AgentSession{Ticket: &tk.Number}}),
	}
	for _, ev := range evs {
		mapped := pluginEvents(ev, map[int]string{}, lookup, "")
		if len(mapped) != 1 {
			t.Fatalf("event %s missing", ev.Kind)
		}
		got := mapped[0].Data["ticket"].(map[string]any)
		if !reflect.DeepEqual(got["folders"], payload["folders"]) {
			t.Fatalf("event %s dropped folders: %+v", ev.Kind, got)
		}
	}
	empty := s.pluginTicketPayload(ctx, api.TicketSummary{Number: 999})
	if folders := empty["folders"].([]any); folders == nil || len(folders) != 0 {
		t.Fatalf("empty %+v", folders)
	}
	disabled := false
	if _, err := s.settings.UpdateProject(p.ID, settings.ProjectUpdate{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pluginProjectContext(ctx, 0, p.ID, nil); err == nil {
		t.Fatal("disabled project selected")
	}
	folder := s.pluginFolder(ctx, tk.Number, p.ID)
	if folder["path"] != "" || folder["reason"] != "project_unavailable" {
		t.Fatalf("disabled %+v", folder)
	}
	if _, err := s.pluginProjectContext(ctx, 0, "nonexistent", nil); err == nil {
		t.Fatal("unknown project selected")
	}
	enabled := true
	if _, err := s.settings.UpdateProject(p.ID, settings.ProjectUpdate{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(p.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pluginProjectContext(ctx, 0, p.ID, nil); err == nil {
		t.Fatal("missing repository selected")
	}
}

func TestPluginSelectedUnavailableFolderRefused(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	maker := newTicketMaker(t, s)
	n := maker.create()
	resp := asUser(b, s, "POST", "/api/plugins/rec/actions/hello", api.PluginRun{Ticket: n, Project: maker.project})
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	resp.Body.Close()
}

func pluginRealPath(t *testing.T, p string) string {
	t.Helper()
	r, e := filepath.EvalSymlinks(p)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestPluginHistoricalEventFoldersMatchHistoricalProjects(t *testing.T) {
	historical := api.TicketSummary{Number: 7, Projects: []api.TicketProject{{Project: "removed"}, {Project: "kept"}}, Attention: "look"}
	current := map[string]any{"folders": []any{
		map[string]any{"project": "new", "kind": "worktree", "path": "/new", "available": true, "reason": ""},
		map[string]any{"project": "kept", "kind": "worktree", "path": "/kept", "available": true, "reason": ""},
	}}
	events := []api.Event{
		boardEvent(t, 1, api.KindTicketCreated, api.TicketCreatedData{Ticket: historical}),
		boardEvent(t, 2, api.KindTicketMoved, api.TicketMovedData{Ticket: historical, From: api.ColumnPlanning, To: api.ColumnReady}),
		boardEvent(t, 3, api.KindTicketUpdated, api.TicketUpdatedData{Ticket: historical}),
	}
	for _, ev := range events {
		mapped := pluginEvents(ev, map[int]string{}, func(int) map[string]any { return current }, "")
		if len(mapped) == 0 {
			t.Fatalf("missing %s", ev.Kind)
		}
		for _, e := range mapped {
			p := e.Data["ticket"].(map[string]any)
			folders := p["folders"].([]any)
			if len(folders) != 2 {
				t.Fatalf("%s folder length: %+v", ev.Kind, folders)
			}
			first := folders[0].(map[string]any)
			second := folders[1].(map[string]any)
			if first["project"] != "removed" || first["path"] != "" || first["available"] != false || first["reason"] == "" || second["project"] != "kept" || second["path"] != "/kept" {
				t.Fatalf("%s snapshot misaligned %+v", ev.Kind, folders)
			}
		}
	}
}
