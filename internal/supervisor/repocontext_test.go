package supervisor

import (
	"strings"
	"testing"
)

func TestRepoContextListsKeysForLandscape(t *testing.T) {
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos = map[string]string{"api": "/w/api", "ui": "/w/ui", "worker": "/w/worker"}
	got := o.Sup.RepoContext()
	for _, want := range []string{"api", "ui", "worker", "/w/api", "--repo"} {
		if !strings.Contains(got, want) {
			t.Errorf("repo context missing %q:\n%s", want, got)
		}
	}
	// It must say this office spans several repositories, so the CEO splits
	// cross-service work instead of assuming one codebase.
	if !strings.Contains(got, "3 repositories") {
		t.Errorf("landscape not described:\n%s", got)
	}
}

func TestRepoContextDescribesSingleRepo(t *testing.T) {
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos = map[string]string{"myapp": "/w/myapp"}
	got := o.Sup.RepoContext()
	if !strings.Contains(got, "single repository") {
		t.Errorf("single-repo office not described:\n%s", got)
	}
	if !strings.Contains(got, "myapp") {
		t.Errorf("repo key missing:\n%s", got)
	}
}

func TestRepoContextWithNoRepos(t *testing.T) {
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos = map[string]string{}
	got := o.Sup.RepoContext()
	if !strings.Contains(got, "no repositories") {
		t.Errorf("empty office not described:\n%s", got)
	}
	if !strings.Contains(got, "omo repo add") || strings.Contains(got, ".omo/omo.yaml") {
		t.Errorf("empty office should direct agents to the safe CLI, not internal config:\n%s", got)
	}
}

// The CEO and PMs decide where work goes, so they get the repo list; a
// developer already sits in its worktree and must not be tempted to wander.
func TestReadyGivesRepoContextToDecidingRolesOnly(t *testing.T) {
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos = map[string]string{"api": "/w/api"}

	for _, role := range []string{"ceo", "product_manager"} {
		name, err := o.Sup.Spawn(role, role, 0, o.Dir, "goal")
		if err != nil {
			t.Fatal(err)
		}
		r, err := o.Sup.ready(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(r.Prompt, "REPOSITORIES") {
			t.Errorf("%s prompt has no repo context:\n%s", role, r.Prompt)
		}
	}
	name, _ := o.Sup.Spawn("developer", "developer", 0, o.Dir, "goal")
	r, _ := o.Sup.ready(name)
	if strings.Contains(r.Prompt, "REPOSITORIES") {
		t.Errorf("developer prompt should not carry the repo list:\n%s", r.Prompt)
	}
}
