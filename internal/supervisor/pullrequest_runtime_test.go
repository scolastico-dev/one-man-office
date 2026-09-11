package supervisor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestPullrequestCreateRunsThroughSupervisorAndRealOmoProcess(t *testing.T) {
	repo := devRepo(t)
	remoteRoot := t.TempDir()
	bare := filepath.Join(remoteRoot, "remote.git")
	gitOutput(t, remoteRoot, "init", "--bare", bare)
	gitOutput(t, repo, "remote", "add", "origin", "https://github.com/acme/runtime.git")
	gitOutput(t, repo, "remote", "set-url", "--push", "origin", "file://"+bare)

	forge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/acme/runtime/pulls" {
			t.Errorf("unexpected forge request: %s %s", r.Method, r.URL.Path)
			return
		}
		_, _ = io.WriteString(w, `{"html_url":"https://github.com/acme/runtime/pull/77"}`)
	}))
	defer forge.Close()

	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo, MergeTarget: config.MergeTargetAsIs}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo-runtime", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "ceo-runtime", "working"); err != nil {
		t.Fatal(err)
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	source := filepath.Join(filepath.Dir(sourceFile), "..", "..", "plugins", "pullrequest")
	target := filepath.Join(o.Dir, plugins.Dir, "pullrequest")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plugin.json", "prompt.lua", "create.lua"} {
		raw, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := plugins.LoadConfigured(o.Dir, o.DB, map[string]plugins.Settings{
		"pullrequest": {Enabled: true, Config: map[string]any{
			"forge": "forgejo", "api_url": forge.URL, "token": "runtime-token",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })

	job := &queue.Job{Title: "runtime request", Goal: "create a runtime pull request", Role: "developer", Repo: "api"}
	if err := o.Sup.Jobs.Create(job); err != nil {
		t.Fatal(err)
	}
	branch := "omo/job-" + itoa(job.ID)
	worktree := filepath.Join(o.Dir, ".omo", "worktrees", "api-"+itoa(job.ID))
	if err := o.Sup.Git.AddWorktree(repo, worktree, branch); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetWorktree(job.ID, worktree, branch); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "runtime.txt"), []byte("runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, worktree, "add", "runtime.txt")
	gitOutput(t, worktree, "commit", "-m", "runtime")
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-runtime", Role: "developer", Profile: "developer", JobID: job.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "developer-runtime", "working"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(o.Dir, ".omo", "omo.lock"), []byte(o.Sup.SocketPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(o.Dir, ".omo", "omo.lock")) })
	t.Setenv("PATH", filepath.Dir(omoBin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	var response proto.PluginTriggerResponse
	if err := sockc.Call(o.Sup.SocketPath, "developer-runtime", "plugin.trigger", proto.PluginTriggerArgs{
		Name: "pullrequest", Action: "create", Args: []string{"Runtime request"},
	}, &response); err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(string)
	if !ok || result != "https://github.com/acme/runtime/pull/77" {
		t.Fatalf("runtime pullrequest result = %q", response.Result)
	}
	if err := gitCheckRef(t, bare, branch); err != nil {
		t.Fatalf("runtime pullrequest did not push branch %q: %v", branch, err)
	}
	for _, recipient := range []string{"user", "ceo-runtime"} {
		mail, err := o.Sup.Mail.Inbox(recipient)
		if err != nil || len(mail) != 1 || !strings.Contains(mail[0].Body, result) {
			t.Fatalf("runtime pullrequest mail for %s = %#v, %v", recipient, mail, err)
		}
		if mail[0].From != bus.SystemSender {
			t.Fatalf("runtime pullrequest mail sender = %q, want %q", mail[0].From, bus.SystemSender)
		}
	}
}
