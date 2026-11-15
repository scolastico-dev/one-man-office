package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

func makeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func seedRun(t *testing.T, scenario string) (map[string]int64, error) {
	t.Helper()
	var out bytes.Buffer
	if err := run(context.Background(), strings.NewReader(scenario), &out); err != nil {
		return nil, err
	}
	var res result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	return res.Refs, nil
}

func TestSeedBuildsEveryColumn(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	repo := makeRepo(t)
	scenario, _ := json.Marshal(map[string]any{
		"projects": []map[string]string{{"path": repo, "name": "demo"}},
		"tickets": []map[string]any{
			{"ref": "plan", "type": "task", "title": "Planning one", "projects": []string{"demo"}},
			{"ref": "ready", "type": "task", "title": "Ready one", "projects": []string{"demo"}, "to": "ready"},
			{"ref": "prog", "type": "task", "title": "In progress one", "projects": []string{"demo"}, "to": "in_progress", "branch": "omo/3-prog"},
			{"ref": "rev", "type": "task", "title": "Review one", "projects": []string{"demo"}, "to": "review", "review": "human"},
			{"ref": "agentrev", "type": "task", "title": "Agent review one", "projects": []string{"demo"}, "to": "review", "review": "agent"},
			{"ref": "done", "type": "task", "title": "Done one", "projects": []string{"demo"}, "to": "done", "review": "human"},
			{"ref": "doneagent", "type": "task", "title": "Done after agents", "projects": []string{"demo"}, "to": "done", "review": "agent_then_human"},
			{"ref": "st", "type": "story", "title": "Story", "projects": []string{"demo"}, "to": "in_progress", "review": "human"},
			{"ref": "sttask", "type": "task", "title": "Story task", "projects": []string{"demo"}, "story": "st", "to": "review", "review": "human"},
		},
	})
	refs, err := seedRun(t, string(scenario))
	if err != nil {
		t.Fatal(err)
	}

	layout, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	ro, err := db.OpenReadOnly(layout.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	st, err := settings.Open(layout.Root, settings.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b := board.New(ro, boardEnv{st: st, git: gitops.New()}, board.Options{})
	want := map[string]struct {
		col   api.Column
		stage api.ReviewStage
	}{
		"plan":      {api.ColumnPlanning, ""},
		"ready":     {api.ColumnReady, ""},
		"prog":      {api.ColumnInProgress, ""},
		"rev":       {api.ColumnReview, api.ReviewStageHuman},
		"agentrev":  {api.ColumnReview, api.ReviewStageAgent},
		"done":      {api.ColumnDone, ""},
		"doneagent": {api.ColumnDone, ""},
		"st":        {api.ColumnInProgress, ""},
		"sttask":    {api.ColumnReview, api.ReviewStageHuman},
	}
	for ref, w := range want {
		n, ok := refs[ref]
		if !ok {
			t.Fatalf("no number for ref %q in %v", ref, refs)
		}
		tk, err := b.Get(context.Background(), board.User, n)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if tk.Column != w.col || tk.ReviewStage != w.stage {
			t.Errorf("%s (#%d): column %s stage %q, want %s %q", ref, n, tk.Column, tk.ReviewStage, w.col, w.stage)
		}
	}
	if tk, _ := b.Get(context.Background(), board.User, refs["sttask"]); tk.Story == nil || int64(*tk.Story) != refs["st"] {
		t.Errorf("story task is not in its story: %v", tk.Story)
	}
	if tk, _ := b.Get(context.Background(), board.User, refs["prog"]); tk.Projects[0].Branch != "omo/3-prog" {
		t.Errorf("branch not recorded: %+v", tk.Projects)
	}
	if tk, _ := b.Get(context.Background(), board.User, refs["done"]); tk.Resolution != api.ResolutionCompleted {
		t.Errorf("done ticket resolution %q", tk.Resolution)
	}
	if paused, err := b.Paused(context.Background()); err != nil || !paused {
		t.Errorf("paused = %v, %v; want true", paused, err)
	}
}

func TestSeedRefusesStoryBeyondInProgress(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	repo := makeRepo(t)
	_, err := seedRun(t, `{"projects":[{"path":"`+repo+`","name":"demo"}],"tickets":[{"ref":"s","type":"story","title":"S","projects":["demo"],"to":"review"}]}`)
	if err == nil || !strings.Contains(err.Error(), "story") {
		t.Fatalf("err = %v", err)
	}
}

func TestSeedRefusesUnsafeHomes(t *testing.T) {
	t.Setenv("OMO_HOME", "")
	if err := run(context.Background(), strings.NewReader(`{}`), &bytes.Buffer{}); err == nil {
		t.Error("ran without OMO_HOME")
	}
	outside := "/nonexistent-omo-seed-home"
	if os.PathSeparator == '\\' {
		t.Skip("path check is for unix homes")
	}
	t.Setenv("OMO_HOME", outside)
	if err := run(context.Background(), strings.NewReader(`{}`), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "temporary") {
		t.Errorf("home outside the temp folder: err = %v", err)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Error("the refused home was created")
	}
}

func TestSeedRefusesHomeWithTickets(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	repo := makeRepo(t)
	sc := `{"projects":[{"path":"` + repo + `","name":"demo"}],"tickets":[{"ref":"a","type":"task","title":"A","projects":["demo"]}]}`
	if _, err := seedRun(t, sc); err != nil {
		t.Fatal(err)
	}
	if _, err := seedRun(t, sc); err == nil || !strings.Contains(err.Error(), "fresh") {
		t.Fatalf("second run: err = %v", err)
	}
}
