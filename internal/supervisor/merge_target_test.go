package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func TestTopLevelFreelancerAsIsRetainsBranchAndNotifiesPRRecipients(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["demo"] = config.Repository{Path: repo, MergeTarget: config.MergeTargetAsIs}
	o.Sup.Cfg.Branches.MergeTarget = config.MergeTargetAutoMerge
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo-ada", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	j := &queue.Job{Title: "publish", Goal: "g", Role: "freelancer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.Transition(j.ID, queue.StateAssigned); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.Transition(j.ID, queue.StateWorking); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(o.Dir, ".omo", "worktrees", "demo-as-is")
	branch := "omo/job-as-is"
	if err := o.Sup.Git.AddWorktree(repo, worktree, branch); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "asis.txt"), []byte("pull request\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGitTest(worktree, "add", "asis.txt"); err != nil {
		t.Fatal(err)
	}
	if err := runGitTest(worktree, "commit", "-m", "asis change"); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetWorktree(j.ID, worktree, branch); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "freelancer-ada", Role: "freelancer", Profile: "freelancer", JobID: j.ID}); err != nil {
		t.Fatal(err)
	}

	if err := o.Sup.done("freelancer-ada", "reported"); err != nil {
		t.Fatal(err)
	}
	got, err := o.Sup.Jobs.Get(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateDone {
		t.Fatalf("state = %s, want done", got.State)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("asis worktree still exists: %v", err)
	}
	if err := runGitTest(repo, "show-ref", "--verify", "refs/heads/"+branch); err != nil {
		t.Fatalf("asis branch was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "asis.txt")); !os.IsNotExist(err) {
		t.Fatalf("asis changed checkout: %v", err)
	}
	mail, err := o.Sup.Mail.Inbox("user")
	if err != nil || len(mail) != 1 || !strings.Contains(mail[0].Body, "pull request") || !strings.Contains(mail[0].Body, branch) {
		t.Fatalf("user PR mail = %#v, err=%v", mail, err)
	}
	mail, err = o.Sup.Mail.Inbox("ceo-ada")
	if err != nil || len(mail) != 1 || !strings.Contains(mail[0].Body, "pull request") {
		t.Fatalf("CEO PR mail = %#v, err=%v", mail, err)
	}
}

func TestTopLevelFreelancerAutoMergeDeletesBranchAfterCheckoutMerge(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["demo"] = config.Repository{Path: repo}
	j := &queue.Job{Title: "merge", Goal: "g", Role: "freelancer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking} {
		if err := o.Sup.Jobs.Transition(j.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	worktree := filepath.Join(o.Dir, ".omo", "worktrees", "demo-auto")
	branch := "omo/job-auto"
	if err := o.Sup.Git.AddWorktree(repo, worktree, branch); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "merged.txt"), []byte("merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGitTest(worktree, "add", "merged.txt"); err != nil {
		t.Fatal(err)
	}
	if err := runGitTest(worktree, "commit", "-m", "merge change"); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetWorktree(j.ID, worktree, branch); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "freelancer-auto", Role: "freelancer", Profile: "freelancer", JobID: j.ID}); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.done("freelancer-auto", "merged"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "merged.txt")); err != nil {
		t.Fatalf("automerge did not update checkout: %v", err)
	}
	if err := runGitTest(repo, "show-ref", "--verify", "refs/heads/"+branch); err == nil {
		t.Fatal("automerge retained branch")
	}
}

func TestPMConflictReturnsToWorkingAndRetainsIntegrationWorktree(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo}
	pm := &queue.Job{Title: "conflict", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking, queue.StateMerging} {
		if err := o.Sup.Jobs.Transition(pm.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	worktree := filepath.Join(o.Dir, ".omo", "worktrees", "api-pm")
	branch := "omo/job-pm-conflict"
	if err := o.Sup.Git.AddWorktree(repo, worktree, branch); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "shared.txt"), []byte("integration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGitTest(worktree, "add", "shared.txt"); err != nil {
		t.Fatal(err)
	}
	if err := runGitTest(worktree, "commit", "-m", "integration"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "shared.txt"), []byte("checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGitTest(repo, "add", "shared.txt"); err != nil {
		t.Fatal(err)
	}
	if err := runGitTest(repo, "commit", "-m", "checkout"); err != nil {
		t.Fatal(err)
	}
	pm.IntegrationBranches = map[string]queue.IntegrationBranch{"api": {Branch: branch, Base: "main", Worktree: worktree}}
	if err := o.Sup.applyMergeTarget(pm); err == nil || !strings.Contains(err.Error(), `repository "api"`) {
		t.Fatalf("conflict error = %v", err)
	}
	got, err := o.Sup.Jobs.Get(pm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateWorking {
		t.Fatalf("PM state = %s, want working", got.State)
	}
	if !strings.Contains(got.Note, "api") {
		t.Fatalf("PM note = %q, want repository name", got.Note)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("integration worktree was removed after conflict: %v", err)
	}
	if err := runGitTest(repo, "show-ref", "--verify", "refs/heads/"+branch); err != nil {
		t.Fatalf("integration branch was removed after conflict: %v", err)
	}
}

func TestPMAsIsCleansWorktreeThenNotifiesUserAndCEO(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo, MergeTarget: config.MergeTargetAsIs}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo-pm", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	pm := &queue.Job{Title: "PR", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking} {
		if err := o.Sup.Jobs.Transition(pm.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	worktree := filepath.Join(o.Dir, ".omo", "worktrees", "api-pm-asis")
	branch := "omo/job-pm-asis"
	if err := o.Sup.Git.AddWorktree(repo, worktree, branch); err != nil {
		t.Fatal(err)
	}
	pm.IntegrationBranches = map[string]queue.IntegrationBranch{"api": {Branch: branch, Base: "develop", Worktree: worktree}}
	if err := o.Sup.finishTopLevelJob(pm, "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("PM asis worktree still exists: %v", err)
	}
	if err := runGitTest(repo, "show-ref", "--verify", "refs/heads/"+branch); err != nil {
		t.Fatalf("PM asis branch deleted: %v", err)
	}
	for _, recipient := range []string{"user", "ceo-pm"} {
		mail, err := o.Sup.Mail.Inbox(recipient)
		if err != nil || len(mail) != 1 {
			t.Fatalf("%s mail = %#v, err=%v", recipient, mail, err)
		}
		for _, want := range []string{"api", branch, "develop", "pull request"} {
			if !strings.Contains(mail[0].Body, want) {
				t.Errorf("%s mail missing %q: %s", recipient, want, mail[0].Body)
			}
		}
	}
}

func runGitTest(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_PARAMETERS='commit.gpgSign=false'")
	return func() error {
		out, err := cmd.CombinedOutput()
		if err != nil {
			return &gitTestError{args: args, output: string(out), err: err}
		}
		return nil
	}()
}

type gitTestError struct {
	args   []string
	output string
	err    error
}

func (e *gitTestError) Error() string {
	return "git " + strings.Join(e.args, " ") + ": " + e.output + ": " + e.err.Error()
}
