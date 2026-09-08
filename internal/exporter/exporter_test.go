package exporter

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/queue"
	_ "modernc.org/sqlite"
)

func TestStatisticsOmitsProjectAndJobDetails(t *testing.T) {
	database, err := sql.Open("sqlite", "file:export-stats?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE jobs (state TEXT); INSERT INTO jobs VALUES ('done'); INSERT INTO jobs VALUES ('failed'); CREATE TABLE agents (role TEXT); INSERT INTO agents VALUES ('developer');`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"events", "incidents", "messages", "model_usage_snapshots", "overall_statistics", "plugin_logs", "plugin_runtime", "plugin_storage", "shutdown_contexts"} {
		if _, err := database.Exec("CREATE TABLE " + table + " (value TEXT)"); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := Statistics(database)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "project") || strings.Contains(string(raw), "title") || strings.Contains(string(raw), "goal") {
		t.Fatalf("statistics leaked detail: %s", raw)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["row_counts"] == nil || decoded["job_states"] == nil {
		t.Fatalf("statistics = %s", raw)
	}
}

func TestGitWritesActiveAndCompletedJobFiles(t *testing.T) {
	database, err := sql.Open("sqlite", "file:export-git?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE jobs (id INTEGER PRIMARY KEY, title TEXT, goal TEXT, role TEXT, model TEXT, repo TEXT, worktree TEXT, branch TEXT, parent_job INTEGER, state TEXT, assignee TEXT, result TEXT, note TEXT, retries INTEGER, review_rejections INTEGER, review_override INTEGER, developer_models TEXT, force_developer_model TEXT, force_model INTEGER); INSERT INTO jobs VALUES (1,'Ship it','project secret','developer','','','','',0,'working','dev','','checkpoint',0,0,0,'[]','',0); INSERT INTO jobs VALUES (2,'Done','done goal','product_manager','','','','',0,'done','pm','','',0,0,0,'[]','',0)`); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	count, err := Git(root, database)
	if err != nil || count != 2 {
		t.Fatalf("export count=%d err=%v", count, err)
	}
	var paths []string
	if err := filepath.Walk(filepath.Join(root, ".omo"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("exported files = %v", paths)
	}
}

func TestDiscoverAndImportExternalJobQueuesItWithoutOldAssignment(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "job.yaml")
	if err := os.WriteFile(path, []byte("id: 42\ntitle: Resume work\ngoal: inspect checkpoint\nrole: developer\nstate: working\ncheckpoint: checkpoint\nassignment: old-agent\n"), 0644); err != nil {
		t.Fatal(err)
	}
	external, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", "file:export-import?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE jobs (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT, goal TEXT, role TEXT, model TEXT, repo TEXT, worktree TEXT, branch TEXT, parent_job INTEGER, state TEXT, assignee TEXT, result TEXT, note TEXT, retries INTEGER, review_rejections INTEGER, review_override INTEGER, developer_models TEXT, force_developer_model TEXT, force_model INTEGER); CREATE TABLE events (id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT, agent TEXT, job_id INTEGER, detail TEXT, created_at TEXT DEFAULT 'now');`); err != nil {
		t.Fatal(err)
	}
	job, err := Import(database, *external)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != queue.StateQueued || job.Assignee != "" || job.Note != "checkpoint" {
		t.Fatalf("imported job = %+v", job)
	}
}
