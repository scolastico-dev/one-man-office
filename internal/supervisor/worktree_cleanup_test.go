package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func TestCancelJobStopsAllAgentsAndRemovesWorktree(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{
		"developer": "ready\nsleep|60s\n",
		"reviewer":  "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.Repos["demo"] = repo
	startDispatch(t, o)
	j := &queue.Job{Title: "cancel me", Goal: "g", Role: "developer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	o.Sup.kickDispatch()
	waitFor(t, 15*time.Second, "developer working", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateWorking && got.Worktree != ""
	})
	got, _ := o.Sup.Jobs.Get(j.ID)
	developer := got.Assignee
	reviewer, err := o.Sup.Spawn("reviewer", "reviewer", j.ID, got.Worktree, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "reviewer working", func() bool {
		return agentState(t, o, reviewer) == "working"
	})

	if err := o.Sup.CancelJob(j.ID, "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got.Worktree); !os.IsNotExist(err) {
		t.Fatalf("cancelled worktree still exists: %v", err)
	}
	for _, name := range []string{developer, reviewer} {
		if state := agentState(t, o, name); state != "dead" {
			t.Fatalf("agent %s state = %s, want dead", name, state)
		}
	}
	got, _ = o.Sup.Jobs.Get(j.ID)
	if got.Worktree != "" || got.Branch != "" {
		t.Fatalf("cancelled job retained worktree metadata: %+v", got)
	}
}

func TestCompletedFreelancerWorktreeRemovedAfterAgentExits(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{
		"freelancer": "ready\ndone|reported\nwait\n",
	})
	o.Sup.Cfg.Repos["demo"] = repo
	startDispatch(t, o)
	j := &queue.Job{Title: "research", Goal: "g", Role: "freelancer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	o.Sup.kickDispatch()
	waitFor(t, 15*time.Second, "freelancer retained after completion", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		a, err := db.GetAgent(o.DB, got.Assignee)
		return err == nil && got.State == queue.StateDone && a.State == "waiting"
	})
	got, _ := o.Sup.Jobs.Get(j.ID)
	if _, err := os.Stat(got.Worktree); err != nil {
		t.Fatalf("retained freelancer lost worktree early: %v", err)
	}
	if err := o.Sup.KillAgent(got.Assignee, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "completed freelancer worktree cleanup", func() bool {
		_, err := os.Stat(got.Worktree)
		return os.IsNotExist(err)
	})
}

func TestCleanupTerminalWorktreesReconcilesOldCancelledJobs(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["demo"] = repo
	j := &queue.Job{Title: "old cancellation", Goal: "g", Role: "developer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.Transition(j.ID, queue.StateCancelled); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(o.Dir, ".omo", "worktrees", "demo-old")
	branch := "omo/job-old"
	if err := o.Sup.Git.AddWorktree(repo, worktree, branch); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetWorktree(j.ID, worktree, branch); err != nil {
		t.Fatal(err)
	}

	if err := o.Sup.CleanupTerminalWorktrees(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("old cancelled worktree still exists: %v", err)
	}
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.Worktree != "" || got.Branch != "" {
		t.Fatalf("old cancelled job retained worktree metadata: %+v", got)
	}
}

func TestCleanupTerminalWorktreesRefusesPathsOutsideOffice(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["demo"] = repo
	j := &queue.Job{Title: "unsafe record", Goal: "g", Role: "developer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.Transition(j.ID, queue.StateCancelled); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "outside-office")
	branch := "omo/outside-office"
	if err := o.Sup.Git.AddWorktree(repo, worktree, branch); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetWorktree(j.ID, worktree, branch); err != nil {
		t.Fatal(err)
	}

	err := o.Sup.CleanupTerminalWorktrees()
	if err == nil || !strings.Contains(err.Error(), "refusing to remove unmanaged worktree") {
		t.Fatalf("cleanup error = %v", err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("unmanaged worktree was removed: %v", err)
	}
}
