package server

import (
	"context"
	"encoding/json"
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

func TestPluginProjectlessWorkingDirectory(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	ctx := t.Context()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "notes"})
	if err != nil {
		t.Fatal(err)
	}
	n := tk.Number
	dir := engine.WorkdirDir(s.opts.Layout.Workdirs, int64(n))
	check := func(path string, available bool) {
		t.Helper()
		current, err := s.board.Get(ctx, board.User, int64(n))
		if err != nil {
			t.Fatal(err)
		}
		payload := s.pluginTicketPayload(ctx, current.TicketSummary)
		if ids := payload["projects"].([]any); ids == nil || len(ids) != 0 {
			t.Fatalf("project compatibility: %+v", ids)
		}
		folders := payload["folders"].([]any)
		if len(folders) != 1 {
			t.Fatalf("working directory context missing: %+v", payload)
		}
		f := folders[0].(map[string]any)
		if f["project"] != "" || f["kind"] != "working_directory" || f["path"] != path || f["available"] != available || (available && f["reason"] != "") || (!available && f["reason"] != "folder_unavailable") {
			t.Fatalf("folder: %+v", f)
		}
	}
	check("", false)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	check("", false) // An existing but unrecorded directory is not authoritative.
	if err := s.board.SetWorkdir(ctx, int64(n), dir); err != nil {
		t.Fatal(err)
	}
	check(pluginRealPath(t, dir), true)
	if err := s.board.SetWorkdir(ctx, int64(n), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	check("", false) // A recorded foreign directory must not leak a launch path.
	if err := s.board.SetWorkdir(ctx, int64(n), dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	check("", false) // Stale record.
	if runtime.GOOS != "windows" {
		if err := os.Symlink(t.TempDir(), dir); err != nil {
			t.Fatal(err)
		}
		check("", false)
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Cancel(ctx, board.User, int64(n), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	check(pluginRealPath(t, dir), true) // Generic context, like terminals, can inspect retained work.
	if err := s.board.ForgetWorkdir(ctx, int64(n), dir); err != nil {
		t.Fatal(err)
	}
	check("", false) // Removal clears the authoritative record even if disk remains.
	p, err := s.settings.AddProject(committedRepo(t, "main"), "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pluginProjectContext(ctx, n, p.ID, nil); err == nil {
		t.Fatal("projectless ticket accepted a repository selection")
	}
}

func TestPluginHistoricalProjectlessFolders(t *testing.T) {
	working := map[string]any{"project": "", "kind": "working_directory", "path": "/work/7", "available": true, "reason": ""}
	project := map[string]any{"project": "repo", "kind": "worktree", "path": "/repo/7", "available": true, "reason": ""}
	for _, c := range []struct {
		name       string
		historical api.TicketSummary
		current    []any
		want       []any
	}{
		{"same working folder", api.TicketSummary{Number: 7, Type: api.TypeTask}, []any{working}, []any{working}},
		{"projectless snapshot cannot borrow a later repository", api.TicketSummary{Number: 7, Type: api.TypeTask}, []any{project}, []any{map[string]any{"project": "", "kind": "working_directory", "path": "", "available": false, "reason": "folder_unavailable"}}},
		{"project snapshot cannot borrow a later working folder", api.TicketSummary{Number: 7, Type: api.TypeTask, Projects: []api.TicketProject{{Project: "repo"}}}, []any{working}, []any{map[string]any{"project": "repo", "kind": "worktree", "path": "", "available": false, "reason": "folder_unavailable"}}},
		{"projectless story has no working folder", api.TicketSummary{Number: 7, Type: api.TypeStory}, []any{working}, []any{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, kind := range []string{api.KindTicketCreated, api.KindTicketMoved, api.KindTicketUpdated} {
				tk := c.historical
				tk.Attention = "look"
				var data any = api.TicketCreatedData{Ticket: tk}
				if kind == api.KindTicketMoved {
					data = api.TicketMovedData{Ticket: tk, From: api.ColumnPlanning, To: api.ColumnReady}
				}
				if kind == api.KindTicketUpdated {
					data = api.TicketUpdatedData{Ticket: tk}
				}
				mapped := pluginEvents(boardEvent(t, 1, kind, data), map[int]string{}, func(int) map[string]any { return map[string]any{"folders": c.current} }, "")
				if len(mapped) == 0 {
					t.Fatalf("missing %s", kind)
				}
				for _, ev := range mapped {
					payload := ev.Data["ticket"].(map[string]any)
					if !reflect.DeepEqual(payload["folders"], c.want) {
						t.Fatalf("%s folders: %+v, want %+v", kind, payload["folders"], c.want)
					}
				}
			}
		})
	}
}

func TestPluginProjectlessManualFolderContext(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "context")
	dir := filepath.Join(s.opts.Layout.Plugins, "context")
	manifest := `{"hooks":[{"event":"manual","name":"inspect","scope":"ticket","description":"Inspect folders","lua":"inspect.lua"}]}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inspect.lua"), []byte(`return {message=omo.json_encode(event.data)}`), 0600); err != nil {
		t.Fatal(err)
	}
	s.reloadPlugins(true)
	tk, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "manual context"})
	if err != nil {
		t.Fatal(err)
	}
	n := int64(tk.Number)
	working := engine.WorkdirDir(s.opts.Layout.Workdirs, n)
	if err := s.board.SetWorkdir(t.Context(), n, working); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(working, 0700); err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, s)
	resp := asUser(b, s, "POST", "/api/plugins/context/actions/inspect", api.PluginRun{Ticket: tk.Number})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	answer := body[api.PluginRunResult](t, resp)
	var payload struct {
		Ticket struct {
			Projects []string `json:"projects"`
			Folders  []struct {
				Project   string `json:"project"`
				Kind      string `json:"kind"`
				Path      string `json:"path"`
				Available bool   `json:"available"`
			} `json:"folders"`
		} `json:"ticket"`
		Project any `json:"project"`
	}
	if err := json.Unmarshal([]byte(answer.Message), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Project != nil || payload.Ticket.Projects == nil || len(payload.Ticket.Projects) != 0 || len(payload.Ticket.Folders) != 1 {
		t.Fatalf("context: %s", answer.Message)
	}
	f := payload.Ticket.Folders[0]
	if f.Project != "" || f.Kind != "working_directory" || !f.Available || f.Path != pluginRealPath(t, working) {
		t.Fatalf("folder: %+v", f)
	}
	for _, field := range []string{"path", "workdir", "folders"} {
		resp := asUser(b, s, "POST", "/api/plugins/context/actions/inspect", map[string]any{"ticket": tk.Number, field: "/foreign"})
		wantStatus(t, resp.StatusCode, http.StatusBadRequest)
		resp.Body.Close()
	}
}

func TestPluginProjectlessSnapshotCannotInferLaterWorktree(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	ctx := t.Context()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "initially projectless"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.settings.AddProject(committedRepo(t, "main"), "later")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{p.ID}
	if _, err := s.board.Update(ctx, board.User, int64(tk.Number), api.UpdateTicket{Projects: api.Some(ids)}); err != nil {
		t.Fatal(err)
	}
	wt := engine.WorktreeDir(s.opts.Layout.Worktrees, p.ID, int64(tk.Number))
	gitIn(t, p.Path, "worktree", "add", "-q", "-b", "later-worktree", wt)
	if err := s.board.SetBranch(ctx, int64(tk.Number), p.ID, "later-worktree", wt); err != nil {
		t.Fatal(err)
	}
	payload := s.pluginTicketPayload(ctx, tk.TicketSummary)
	folders := payload["folders"].([]any)
	if len(folders) != 1 {
		t.Fatalf("missing working folder context: %+v", payload)
	}
	f := folders[0].(map[string]any)
	if f["kind"] != "working_directory" || f["path"] != "" || f["available"] != false {
		t.Fatalf("projectless snapshot borrowed later repository: %+v", f)
	}
}
