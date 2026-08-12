package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "."}, {"commit", "-m", "init", "--allow-empty"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func startDispatch(t *testing.T, o *office) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go o.Sup.DispatchLoop(ctx)
}

func TestDispatchSpawnsFreelancerJob(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\ndone|ok\n",
	})
	startDispatch(t, o)
	j := &queue.Job{Title: "odd job", Goal: "do it", Role: "freelancer"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	waitFor(t, 15*time.Second, "job done via dispatch", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateDone
	})
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.Assignee == "" {
		t.Fatal("assignee not recorded")
	}
}

func TestDeveloperJobGetsWorktree(t *testing.T) {
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644)
	gitInit(t, repo)
	o := newOffice(t, map[string]string{
		"developer": "ready\nshell|git rev-parse --abbrev-ref HEAD\nwait\n",
	})
	o.Sup.Cfg.Repos["demo"] = repo
	startDispatch(t, o)
	j := &queue.Job{Title: "dev", Goal: "g", Role: "developer", Repo: "demo"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	waitFor(t, 15*time.Second, "developer working in worktree", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		if got.State != queue.StateWorking || got.Worktree == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(got.Worktree, "README.md"))
		return err == nil
	})
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.Branch != "omo/job-"+itoa(j.ID) {
		t.Fatalf("branch = %q", got.Branch)
	}
}

func TestFreelancerJobCanGetWorktree(t *testing.T) {
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644)
	gitInit(t, repo)
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nwait\n",
	})
	o.Sup.Cfg.Repos["demo"] = repo
	startDispatch(t, o)
	j := &queue.Job{Title: "research", Goal: "inspect the repo", Role: "freelancer", Repo: "demo"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	waitFor(t, 15*time.Second, "freelancer working in worktree", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		if got.State != queue.StateWorking || got.Worktree == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(got.Worktree, "README.md"))
		return err == nil
	})
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.Branch != "omo/job-"+itoa(j.ID) {
		t.Fatalf("branch = %q", got.Branch)
	}
}

func TestConcurrencyCap(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|10s\n",
	})
	o.Sup.Cfg.Limits.MaxFreelancers = 1
	startDispatch(t, o)
	j1 := &queue.Job{Title: "a", Goal: "g", Role: "freelancer"}
	j2 := &queue.Job{Title: "b", Goal: "g", Role: "freelancer"}
	o.Sup.Jobs.Create(j1)
	o.Sup.Jobs.Create(j2)
	o.Sup.kickDispatch()
	waitFor(t, 10*time.Second, "first job assigned", func() bool {
		got, _ := o.Sup.Jobs.Get(j1.ID)
		return got.State != queue.StateQueued
	})
	time.Sleep(2 * time.Second) // dispatch had plenty of chances
	got2, _ := o.Sup.Jobs.Get(j2.ID)
	if got2.State != queue.StateQueued {
		t.Fatalf("second job must stay queued under cap, is %s", got2.State)
	}
}

func TestRetainedFreelancersDoNotConsumeJobCapacity(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\ndone|reported\nwait\n",
	})
	o.Sup.Cfg.Limits.MaxFreelancers = 1
	startDispatch(t, o)
	j1 := &queue.Job{Title: "a", Goal: "g", Role: "freelancer"}
	j2 := &queue.Job{Title: "b", Goal: "g", Role: "freelancer"}
	o.Sup.Jobs.Create(j1)
	o.Sup.Jobs.Create(j2)
	o.Sup.kickDispatch()
	waitFor(t, 15*time.Second, "both jobs completed", func() bool {
		first, _ := o.Sup.Jobs.Get(j1.ID)
		second, _ := o.Sup.Jobs.Get(j2.ID)
		return first.State == queue.StateDone && second.State == queue.StateDone
	})
	if agents, _ := db.LivingByRole(o.DB, "freelancer"); len(agents) != 2 {
		t.Fatalf("retained freelancers = %d, want 2", len(agents))
	}
}

func TestJobCreateGating(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":        "ready\nsleep|30s\n",
		"freelancer": "ready\nsleep|30s\n",
	})
	ceo, _ := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	waitFor(t, 5*time.Second, "ceo up", func() bool { return agentState(t, o, ceo) == "working" })
	free, _ := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "idle")
	waitFor(t, 5*time.Second, "freelancer up", func() bool { return agentState(t, o, free) == "working" })

	var res proto.JobCreateResponse
	// CEO may create a PM job.
	if err := sockc.Call(o.Sup.SocketPath, ceo, "job.create",
		proto.JobCreateArgs{Title: "spec", Goal: "g", Role: "product_manager"}, &res); err != nil {
		t.Fatalf("ceo create pm job: %v", err)
	}
	// CEO may not use a non-selectable model.
	o.Sup.Cfg.Models["locked"] = o.Sup.Cfg.Models["ceo"]
	locked := o.Sup.Cfg.Models["locked"]
	no := false
	locked.Selectable = &no
	o.Sup.Cfg.Models["locked"] = locked
	if err := sockc.Call(o.Sup.SocketPath, ceo, "job.create",
		proto.JobCreateArgs{Title: "x", Goal: "g", Role: "freelancer", Model: "locked"}, nil); err == nil {
		t.Fatal("non-selectable model must be rejected")
	}
	if err := sockc.Call(o.Sup.SocketPath, ceo, "job.create",
		proto.JobCreateArgs{Title: "x", Goal: "g", Role: "product_manager", ForceDeveloperModel: "   "}, nil); err == nil {
		t.Fatal("blank forced developer model must be rejected")
	}
	// Freelancers may not create jobs.
	if err := sockc.Call(o.Sup.SocketPath, free, "job.create",
		proto.JobCreateArgs{Title: "x", Goal: "g", Role: "developer", Repo: "demo"}, nil); err == nil {
		t.Fatal("freelancer job.create must be rejected")
	}
	// Developer jobs need a known repo.
	if err := sockc.Call(o.Sup.SocketPath, ceo, "job.create",
		proto.JobCreateArgs{Title: "x", Goal: "g", Role: "developer", Repo: "ghost"}, nil); err == nil {
		t.Fatal("unknown repo must be rejected")
	}
	if err := sockc.Call(o.Sup.SocketPath, ceo, "job.create",
		proto.JobCreateArgs{Title: "x", Goal: "g", Role: "freelancer", Repo: "ghost"}, nil); err == nil {
		t.Fatal("unknown freelancer repo must be rejected")
	}
}

func TestCEOCanForceDeveloperModelWithoutChangingPMModel(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":             "ready\nsleep|30s\n",
		"product_manager": "ready\nsleep|30s\n",
	})
	o.Sup.Cfg.Repos["demo"] = t.TempDir()
	o.Sup.Cfg.Models["alternate"] = o.Sup.Cfg.Models["developer"]
	ceo, _ := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	waitFor(t, 5*time.Second, "ceo up", func() bool { return agentState(t, o, ceo) == "working" })
	var pmJob proto.JobCreateResponse
	if err := sockc.Call(o.Sup.SocketPath, ceo, "job.create", proto.JobCreateArgs{
		Title: "spec", Goal: "deliver", Role: "product_manager", Model: "product_manager", ForceDeveloperModel: "alternate",
	}, &pmJob); err != nil {
		t.Fatal(err)
	}
	stored, _ := o.Sup.Jobs.Get(pmJob.ID)
	if stored.Model != "product_manager" || stored.ForceDeveloperModel != "alternate" {
		t.Fatalf("PM model/policy not kept separately: %+v", stored)
	}
	pm, err := o.Sup.Spawn("product_manager", "product_manager", pmJob.ID, o.Dir, "deliver")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "PM up", func() bool { return agentState(t, o, pm) == "working" })
	var devJob proto.JobCreateResponse
	if err := sockc.Call(o.Sup.SocketPath, pm, "job.create", proto.JobCreateArgs{
		Title: "dev", Goal: "build", Role: "developer", Repo: "demo",
	}, &devJob); err != nil {
		t.Fatal(err)
	}
	dev, _ := o.Sup.Jobs.Get(devJob.ID)
	if dev.Model != "alternate" {
		t.Fatalf("forced developer model = %q", dev.Model)
	}
	if err := sockc.Call(o.Sup.SocketPath, pm, "job.create", proto.JobCreateArgs{
		Title: "wrong", Goal: "build", Role: "developer", Repo: "demo", Model: "developer",
	}, nil); err == nil {
		t.Fatal("PM bypassed forced developer model")
	}
}

func TestCEOCanLimitPMDeveloperModels(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":             "ready\nsleep|30s\n",
		"product_manager": "ready\nsleep|30s\n",
	})
	o.Sup.Cfg.Repos["demo"] = t.TempDir()
	o.Sup.Cfg.Models["alternate"] = o.Sup.Cfg.Models["developer"]
	ceo, _ := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	waitFor(t, 5*time.Second, "ceo up", func() bool { return agentState(t, o, ceo) == "working" })
	var pmJob proto.JobCreateResponse
	if err := sockc.Call(o.Sup.SocketPath, ceo, "job.create", proto.JobCreateArgs{
		Title: "spec", Goal: "deliver", Role: "product_manager", DeveloperModels: []string{"alternate"},
	}, &pmJob); err != nil {
		t.Fatal(err)
	}
	pm, _ := o.Sup.Spawn("product_manager", "product_manager", pmJob.ID, o.Dir, "deliver")
	waitFor(t, 5*time.Second, "PM up", func() bool { return agentState(t, o, pm) == "working" })
	if err := sockc.Call(o.Sup.SocketPath, pm, "job.create", proto.JobCreateArgs{
		Title: "default denied", Goal: "build", Role: "developer", Repo: "demo",
	}, nil); err == nil {
		t.Fatal("developer default outside allow-list was accepted")
	}
	if err := sockc.Call(o.Sup.SocketPath, pm, "job.create", proto.JobCreateArgs{
		Title: "allowed", Goal: "build", Role: "developer", Repo: "demo", Model: "alternate",
	}, nil); err != nil {
		t.Fatalf("allowed model rejected: %v", err)
	}
}

func TestDeathRequeuesWithRestartNote(t *testing.T) {
	// The fake agent exits right after ready without done → unexpected death.
	o := newOffice(t, map[string]string{
		"freelancer": "ready\n",
	})
	startDispatch(t, o)
	j := &queue.Job{Title: "flaky", Goal: "g", Role: "freelancer"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	waitFor(t, 20*time.Second, "job requeued or failed after death", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return (got.State == queue.StateQueued && got.Note == queue.RestartNote) || got.State == queue.StateFailed
	})
	// It keeps dying → eventually failed with mail to user.
	waitFor(t, 60*time.Second, "job failed after retry limit", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateFailed
	})
	if n, _ := o.Sup.Mail.UnreadCount("user"); n == 0 {
		t.Fatal("expected failure mail to user")
	}
	inbox, _ := o.Sup.Mail.Inbox("user")
	if len(inbox) == 0 || inbox[0].From != bus.SystemSender {
		t.Fatalf("failure mail sender = %+v", inbox)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
