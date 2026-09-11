package cli

import (
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/queue"
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
