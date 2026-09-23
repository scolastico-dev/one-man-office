package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func TestFormatIntegrationBranchesIsDeterministic(t *testing.T) {
	got := formatIntegrationBranches(map[string]queue.IntegrationBranch{
		"zeta":  {Branch: "z", Base: "main", Worktree: "/z"},
		"alpha": {Branch: "a", Base: "develop", Worktree: "/a"},
	})
	if strings.Index(got, "alpha:") > strings.Index(got, "zeta:") {
		t.Fatalf("integration branches were not sorted:\n%s", got)
	}
	for _, want := range []string{"branch: a", "base: develop", "worktree: /a", "branch: z", "base: main", "worktree: /z"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendering missing %q:\n%s", want, got)
		}
	}
}

func TestJobShowIncludesRecordedPullRequests(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "omo.sock")
	srv := sockd.New(socket, nil)
	srv.Handle("job.show", func(_ string, raw json.RawMessage) (any, error) {
		var args map[string]any
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		return queue.Job{
			ID: 42, Title: "pull request visibility", Goal: "show it", Role: "developer",
			State: queue.StateDone, IntegrationBranches: map[string]queue.IntegrationBranch{},
			PullRequests: []queue.PullRequest{
				{Repo: "api", URL: "https://forge.example/acme/api/pulls/12", State: "created"},
				{Repo: "web", URL: "https://forge.example/acme/web/pulls/7", State: "updated"},
			},
		}, nil
	})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Setenv("OMO_SOCKET", socket)
	t.Setenv("OMO_AGENT_ID", "ceo-ada")

	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"job", "show", "42"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job show: %v\n%s", err, out.String())
	}
	want := "id: 42\ntitle: pull request visibility\nrole: developer\nmodel: \nforce_model: false\nstate: done\nassignee: \nrepo: \nmerge_target: \nbranch: \nparent: 0\ndeveloper_models: \nforce_developer_model: \nnote: \nresult: \nintegration_branches:\n  []\npull_requests:\napi: https://forge.example/acme/api/pulls/12 (created)\nweb: https://forge.example/acme/web/pulls/7 (updated)\ngoal:\nshow it\n"
	if got := out.String(); got != want {
		t.Fatalf("job show output =\n%s\nwant =\n%s", got, want)
	}
}
