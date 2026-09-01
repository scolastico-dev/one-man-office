package supervisor

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoContextListsKeysForLandscape(t *testing.T) {
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos = map[string]string{"api": "/w/api", "ui": "/w/ui", "worker": "/w/worker"}
	got := o.Sup.RepoContext()
	for _, want := range []string{"api", "ui", "worker", "--repo"} {
		if !strings.Contains(got, want) {
			t.Errorf("repo context missing %q:\n%s", want, got)
		}
	}
	for _, duplicate := range []string{"/w/api", "/w/ui", "/w/worker"} {
		if strings.Contains(got, duplicate) {
			t.Errorf("repo context repeated path %q already provided by common paths:\n%s", duplicate, got)
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
		if count := strings.Count(r.Prompt, "/w/api"); count != 1 {
			t.Errorf("%s prompt contains repository path %d times, want once:\n%s", role, count, r.Prompt)
		}
	}
	name, _ := o.Sup.Spawn("developer", "developer", 0, o.Dir, "goal")
	r, _ := o.Sup.ready(name)
	if strings.Contains(r.Prompt, "REPOSITORIES IN THIS OFFICE") {
		t.Errorf("developer prompt should not carry the repo list:\n%s", r.Prompt)
	}
	for _, want := range []string{"REFERENCE PATHS", "office_root", "omo_dir", "storage", "workspace", "repo:api", "/w/api"} {
		if !strings.Contains(r.Prompt, want) {
			t.Errorf("developer prompt missing path reference %q:\n%s", want, r.Prompt)
		}
	}
}

func TestPromptPathsCoalesceDuplicateDirectories(t *testing.T) {
	o := newOffice(t, map[string]string{})
	storage := filepath.Join(o.Dir, ".omo", "storage")
	o.Sup.Cfg.Repos = map[string]string{
		"office":  o.Dir,
		"storage": storage,
		"other":   filepath.Join(o.Dir, "other"),
	}
	refs := o.Sup.PromptPaths(storage)
	seen := map[string]bool{}
	labels := map[string]string{}
	for _, ref := range refs {
		if seen[ref.Path] {
			t.Errorf("duplicate path %q remained in references: %+v", ref.Path, refs)
		}
		seen[ref.Path] = true
		labels[ref.Path] = ref.Label
	}
	if got := labels[filepath.Clean(o.Dir)]; !strings.Contains(got, "office_root") || !strings.Contains(got, "repo:office") {
		t.Fatalf("office aliases were not combined: %q", got)
	}
	if got := labels[filepath.Clean(storage)]; !strings.Contains(got, "storage") || !strings.Contains(got, "workspace") || !strings.Contains(got, "repo:storage") {
		t.Fatalf("storage aliases were not combined: %q", got)
	}
}
