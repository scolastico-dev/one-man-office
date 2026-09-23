package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

func TestJobShowCapacityDeferralOnlyForQueuedJob(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		want  bool
	}{
		{name: "queued", state: "queued", want: true},
		{name: "assigned stale fields", state: "assigned"},
		{name: "queued without deferral", state: "queued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			officeDir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(officeDir, ".omo"), 0o755); err != nil {
				t.Fatal(err)
			}
			sock, _, cleanup, err := transport.Endpoint(officeDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			srv := sockd.New(sock, nil)
			srv.Handle("job.show", func(_ string, _ json.RawMessage) (any, error) {
				job := map[string]any{"ID": 7, "Title": "capacity test", "State": tc.state}
				if tc.name != "queued without deferral" {
					job["capacity_deferral_reason"] = "capacity"
					job["capacity_retry_at"] = "2026-09-23T12:34:56Z"
				}
				return job, nil
			})
			if err := srv.Listen(); err != nil {
				t.Fatal(err)
			}
			go srv.Serve()
			t.Cleanup(func() { _ = srv.Close() })
			t.Setenv("OMO_SOCKET", sock)
			t.Setenv("OMO_AGENT_ID", "ceo-ada")

			cmd := Root("test")
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"job", "show", "7"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			for _, fragment := range []string{"capacity_deferral_reason: capacity", "capacity_retry_at: 2026-09-23T12:34:56Z"} {
				if strings.Contains(got, fragment) != tc.want {
					t.Errorf("presence of %q = %t, want %t in:\n%s", fragment, strings.Contains(got, fragment), tc.want, got)
				}
			}
			if !strings.Contains(got, "title: capacity test") || !strings.Contains(got, "goal:") {
				t.Errorf("existing job fields missing:\n%s", got)
			}
		})
	}
}

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
