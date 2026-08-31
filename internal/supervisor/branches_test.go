package supervisor

import (
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func TestGeneratedBranchNameUsesConfiguredPrefix(t *testing.T) {
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Branches.Prefix = "team/job-"
	j := &queue.Job{ID: 42, Role: "developer"}
	got, err := o.Sup.branchNameForJob(j)
	if err != nil {
		t.Fatal(err)
	}
	if got != "team/job-42" {
		t.Fatalf("branch = %q", got)
	}
}

func TestAIBranchNameLaunchesOneShotAgent(t *testing.T) {
	o := newOffice(t, map[string]string{
		"developer": "ready\nbranchname|feat-add-search-index\nsleep|10s\n",
	})
	o.Sup.Cfg.Branches.Prefix = "omo/job-"
	o.Sup.Cfg.Branches.Naming = "ai"
	j := &queue.Job{Title: "Search", Goal: "Add a customer search index", Role: "developer", Repo: "api"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	got, err := o.Sup.branchNameForJob(j)
	if err != nil {
		t.Fatal(err)
	}
	if got != "omo/job-feat-add-search-index" {
		t.Fatalf("branch = %q", got)
	}
	events, _ := db.EventsSince(o.DB, 0)
	found := false
	for _, event := range events {
		found = found || event.Kind == "branch_name_generated" && event.JobID == j.ID
	}
	if !found {
		t.Fatalf("branch_name_generated event missing: %+v", events)
	}
}

func TestValidBranchSuffixRequiresConventionalType(t *testing.T) {
	for _, valid := range []string{"feat-add-auth", "fix-api-timeout", "ci-update-actions"} {
		if !validBranchSuffix(valid) {
			t.Errorf("valid suffix rejected: %q", valid)
		}
	}
	for _, invalid := range []string{"add-auth", "feat/add-auth", "Feat-add-auth", "feat--auth", "feat-"} {
		if validBranchSuffix(invalid) {
			t.Errorf("invalid suffix accepted: %q", invalid)
		}
	}
}
