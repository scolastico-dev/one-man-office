package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestEnsurePMIntegrationWorktreeCreatesAndReusesPerRepository(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo}
	pm := &queue.Job{Title: "pm", Goal: "coordinate", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	got, err := o.Sup.ensurePMIntegrationWorktree(pm.ID, "api")
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(o.Dir, ".omo", "worktrees", "api-pm-"+itoa(pm.ID))
	if got.Branch != "omo/job-pm-"+itoa(pm.ID) || got.Base != "main" || got.Worktree != wantPath {
		t.Fatalf("integration branch = %#v, want branch/base/path", got)
	}
	if _, err := exec.Command("git", "-C", got.Worktree, "rev-parse", "--verify", "HEAD").Output(); err != nil {
		t.Fatalf("integration worktree unavailable: %v", err)
	}
	if branch := strings.TrimSpace(gitOutput(t, repo, "branch", "--show-current")); branch != "main" {
		t.Fatalf("checkout branch changed to %q", branch)
	}
	reused, err := o.Sup.ensurePMIntegrationWorktree(pm.ID, "api")
	if err != nil || reused != got {
		t.Fatalf("repeated initialization = %#v, %v; want %#v", reused, err, got)
	}
	stored, err := o.Sup.Jobs.Get(pm.ID)
	if err != nil || stored.IntegrationBranches["api"] != got {
		t.Fatalf("stored integration branch = %#v, %v", stored.IntegrationBranches, err)
	}
}

func TestEnsurePMIntegrationWorktreeAINameRetainsDeterministicPrefix(t *testing.T) {
	o := newOffice(t, map[string]string{
		"smokealarm": "ready\nbranchname|feat/add-search-index\nsleep|10s\n",
	})
	o.Sup.Cfg.Branches.Naming = "ai"
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: devRepo(t)}
	pm := &queue.Job{Title: "pm", Goal: "coordinate", Role: "product_manager", Repo: "api"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	got, err := o.Sup.ensurePMIntegrationWorktree(pm.ID, "api")
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := "omo/job-pm-" + itoa(pm.ID) + "-"
	if !strings.HasPrefix(got.Branch, wantPrefix) || !strings.HasSuffix(got.Branch, "feat/add-search-index") {
		t.Fatalf("AI integration branch = %q, want prefix %q and validated suffix", got.Branch, wantPrefix)
	}
}

func TestEnsurePMIntegrationWorktreeSupportsMultipleRepositories(t *testing.T) {
	o := newOffice(t, map[string]string{})
	api, web := devRepo(t), devRepo(t)
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: api}
	o.Sup.Cfg.Repos["web"] = config.Repository{Path: web}
	pm := &queue.Job{Title: "pm", Goal: "coordinate", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"api", "web"} {
		if _, err := o.Sup.ensurePMIntegrationWorktree(pm.ID, repo); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := o.Sup.Jobs.Get(pm.ID)
	if err != nil || len(stored.IntegrationBranches) != 2 {
		t.Fatalf("stored repositories = %#v, %v", stored.IntegrationBranches, err)
	}
}

func TestEnsurePMIntegrationWorktreeSerializesPMsOnOneRepository(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo}
	pms := []*queue.Job{
		{Title: "pm one", Goal: "coordinate", Role: "product_manager"},
		{Title: "pm two", Goal: "coordinate", Role: "product_manager"},
	}
	for _, pm := range pms {
		if err := o.Sup.Jobs.Create(pm); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(pms))
	for _, pm := range pms {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := o.Sup.ensurePMIntegrationWorktree(pm.ID, "api")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, pm := range pms {
		stored, err := o.Sup.Jobs.Get(pm.ID)
		if err != nil || len(stored.IntegrationBranches) != 1 {
			t.Fatalf("PM %d integration map = %#v, %v", pm.ID, stored.IntegrationBranches, err)
		}
	}
	if branch := strings.TrimSpace(gitOutput(t, repo, "branch", "--show-current")); branch != "main" {
		t.Fatalf("checkout branch changed to %q", branch)
	}
}

func TestPMChildBranchesFromAndMergesIntoIntegrationWorktree(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{
		"developer": "ready\nshell|echo child > child.txt && git add child.txt && git commit -m child\ndone|built\nwait\n",
		"reviewer":  "ready\nverdict|merge|approved\n",
	})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo}
	pm := &queue.Job{Title: "pm", Goal: "coordinate", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	integration, err := o.Sup.ensurePMIntegrationWorktree(pm.ID, "api")
	if err != nil {
		t.Fatal(err)
	}
	startDispatch(t, o)
	child := &queue.Job{Title: "child", Goal: "build child", Role: "developer", Repo: "api", ParentJob: pm.ID}
	if err := o.Sup.Jobs.Create(child); err != nil {
		t.Fatal(err)
	}
	o.Sup.kickDispatch()
	waitFor(t, 60*time.Second, "PM child merged", func() bool {
		got, _ := o.Sup.Jobs.Get(child.ID)
		return got.State == queue.StateDone
	})
	waitFor(t, 60*time.Second, "PM child cleanup boundary", func() bool {
		var count int
		_ = o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'job_merged' AND job_id = ?`, child.ID).Scan(&count)
		return count > 0
	})
	if _, err := exec.Command("test", "-f", filepath.Join(integration.Worktree, "child.txt")).Output(); err != nil {
		t.Fatalf("child did not merge into PM worktree: %v", err)
	}
	if _, err := exec.Command("test", "-e", filepath.Join(repo, "child.txt")).Output(); err == nil {
		t.Fatal("PM child unexpectedly merged into repository checkout")
	}
}

func TestPMReadyPromptUsesInitializedIntegrationPath(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo}
	pm := &queue.Job{Title: "pm", Goal: "coordinate", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	integration, err := o.Sup.ensurePMIntegrationWorktree(pm.ID, "api")
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := o.Sup.renderRolePrompt("pm", "product_manager", pm.Goal, pm.ID, filepath.Join(o.Dir, ".omo", "storage"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "repo:api") || !strings.Contains(prompt, integration.Worktree) {
		t.Fatalf("PM prompt does not reference integration worktree: %s", prompt)
	}
	if strings.Contains(prompt, "repo:api"+"\n  "+repo) {
		t.Fatalf("PM prompt still points repo:api at checkout: %s", prompt)
	}
}

func TestAuthenticatedPMJobCreateInitializesIntegrationBeforeResponse(t *testing.T) {
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: devRepo(t)}
	o.Sup.Cfg.Repos["web"] = config.Repository{Path: devRepo(t)}
	pm := &queue.Job{Title: "pm", Goal: "coordinate", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "pm-agent", Role: "product_manager", Profile: "product_manager", JobID: pm.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "pm-agent", "working"); err != nil {
		t.Fatal(err)
	}
	create := func(repo string) int64 {
		t.Helper()
		var response proto.JobCreateResponse
		if err := sockc.Call(o.Sup.SocketPath, "pm-agent", "job.create", proto.JobCreateArgs{
			Role: "developer", Repo: repo, Title: "child " + repo, Goal: "build " + repo,
		}, &response); err != nil {
			t.Fatal(err)
		}
		return response.ID
	}
	first := create("api")
	second := create("api")
	third := create("web")
	for _, id := range []int64{first, second, third} {
		child, err := o.Sup.Jobs.Get(id)
		if err != nil || child.ParentJob != pm.ID {
			t.Fatalf("child %d = %#v, %v", id, child, err)
		}
	}
	stored, err := o.Sup.Jobs.Get(pm.ID)
	if err != nil || len(stored.IntegrationBranches) != 2 {
		t.Fatalf("PM integration map = %#v, %v", stored.IntegrationBranches, err)
	}
	for repo, entry := range stored.IntegrationBranches {
		if _, err := exec.Command("git", "-C", entry.Worktree, "rev-parse", "--verify", "HEAD").Output(); err != nil {
			t.Fatalf("%s integration worktree missing before job.create response: %v", repo, err)
		}
	}
}

func TestRecoverIntegrationWorktreesPreservesDurableBranch(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo}
	pm := &queue.Job{Title: "pm", Goal: "coordinate", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	want, err := o.Sup.ensurePMIntegrationWorktree(pm.ID, "api")
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.RecoverIntegrationWorktrees(); err != nil {
		t.Fatal(err)
	}
	got, err := o.Sup.Jobs.Get(pm.ID)
	if err != nil || got.IntegrationBranches["api"] != want {
		t.Fatalf("recovery changed durable entry: %#v, %v", got.IntegrationBranches, err)
	}
	if branch := strings.TrimSpace(gitOutput(t, repo, "branch", "--show-current")); branch != "main" {
		t.Fatalf("recovery changed checkout branch to %q", branch)
	}
}

func TestRecoverIntegrationWorktreesRejectsUnmanagedPath(t *testing.T) {
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: devRepo(t)}
	pm := &queue.Job{Title: "pm", Goal: "coordinate", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetIntegrationBranch(pm.ID, "api", queue.IntegrationBranch{
		Branch: "omo/job-pm-1", Base: "main", Worktree: filepath.Join(o.Dir, "outside"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.RecoverIntegrationWorktrees(); err == nil || !strings.Contains(err.Error(), "unmanaged worktree") {
		t.Fatalf("recovery error = %v, want unmanaged path rejection", err)
	}
}

func TestPMChildMergeConflictAbortsAndReturnsDeveloperToRework(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo}
	pm := &queue.Job{Title: "pm", Goal: "coordinate", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	integration, err := o.Sup.ensurePMIntegrationWorktree(pm.ID, "api")
	if err != nil {
		t.Fatal(err)
	}
	child := &queue.Job{Title: "child", Goal: "change README", Role: "developer", Repo: "api", ParentJob: pm.ID}
	if err := o.Sup.Jobs.Create(child); err != nil {
		t.Fatal(err)
	}
	childBranch := "omo/job-" + itoa(child.ID)
	childWorktree := filepath.Join(o.Dir, ".omo", "worktrees", "api-"+itoa(child.ID))
	if err := o.Sup.Git.AddWorktreeFromBase(repo, childWorktree, childBranch, integration.Branch); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetWorktree(child.ID, childWorktree, childBranch); err != nil {
		t.Fatal(err)
	}
	for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking, queue.StateReview} {
		if err := o.Sup.Jobs.Transition(child.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.Sup.Jobs.SetAssignee(child.ID, "developer-one"); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-one", Role: "developer", Profile: "developer", JobID: child.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "developer-one", "working"); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "reviewer-one", Role: "reviewer", Profile: "reviewer", JobID: child.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "reviewer-one", "working"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childWorktree, "README.md"), []byte("child\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, childWorktree, "add", "README.md")
	gitOutput(t, childWorktree, "commit", "-m", "child conflict")
	if err := os.WriteFile(filepath.Join(integration.Worktree, "README.md"), []byte("integration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, integration.Worktree, "commit", "-am", "integration conflict")
	job, err := o.Sup.Jobs.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = o.Sup.mergeVerdict(&db.Agent{Name: "reviewer-one", Role: "reviewer", JobID: child.ID}, job, "")
	if err == nil {
		t.Fatal("conflicting PM merge unexpectedly succeeded")
	}
	got, err := o.Sup.Jobs.Get(child.ID)
	if err != nil || got.State != queue.StateRework {
		t.Fatalf("conflicting child state = %s, %v; want rework", got.State, err)
	}
	if strings.Contains(gitOutput(t, integration.Worktree, "status", "--porcelain"), "UU") {
		t.Fatal("PM integration worktree left mid-merge")
	}
	mail, err := o.Sup.Mail.Inbox("developer-one")
	if err != nil || len(mail) == 0 || !strings.Contains(mail[len(mail)-1].Body, "merge conflict") {
		t.Fatalf("developer conflict mail = %#v, %v", mail, err)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
