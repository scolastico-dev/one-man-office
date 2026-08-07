package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func devRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644)
	gitInit(t, repo)
	return repo
}

func TestFullReviewMergeFlow(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{
		"developer": "ready\nshell|echo hello > hello.txt && git add hello.txt && git commit -m feat\ndone|built hello.txt\nwait\n",
		"reviewer":  "ready\nverdict|merge|clean work\ndone|merged\n",
	})
	o.Sup.Cfg.Repos["demo"] = repo
	startDispatch(t, o)
	j := &queue.Job{Title: "hello", Goal: "create hello.txt", Role: "developer", Repo: "demo"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	waitFor(t, 60*time.Second, "job done after merge", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateDone
	})
	// Merge landed on main.
	if _, err := os.Stat(filepath.Join(repo, "hello.txt")); err != nil {
		t.Fatal("hello.txt not merged to main")
	}
	// Worktree cleaned up.
	got, _ := o.Sup.Jobs.Get(j.ID)
	if _, err := os.Stat(got.Worktree); !os.IsNotExist(err) {
		t.Fatal("worktree not removed after merge")
	}
	// Branch deleted.
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", got.Branch).CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("branch still exists: %s", out)
	}
}

func TestRejectSendsJobToRework(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{
		// Developer: work, done, wait; after wake: fix, done again, wait.
		"developer": "ready\nshell|echo v1 > f.txt && git add f.txt && git commit -m v1\ndone|v1\nwait\nshell|echo v2 > f.txt && git commit -am v2\ndone|v2\nwait\n",
		// Every reviewer rejects; omo spawns a fresh one for each round.
		"reviewer": "ready\nverdict|reject|needs v2\ndone|rejected\n",
	})
	o.Sup.Cfg.Repos["demo"] = repo
	startDispatch(t, o)
	j := &queue.Job{Title: "f", Goal: "write f.txt", Role: "developer", Repo: "demo"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	// After the reject the job cycles rework → review (developer redoes work
	// and calls done again) and a fresh reviewer rejects again — we assert
	// the rework transition happened and the note carries the findings.
	waitFor(t, 60*time.Second, "job reaches rework with findings note", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.Note == "needs v2"
	})
	waitFor(t, 60*time.Second, "job back in review after rework", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateReview
	})
}

func TestVerdictGating(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{
		"developer": "ready\nshell|echo x > x.txt && git add x.txt && git commit -m x\ndone|x\nwait\n",
		"reviewer":  "ready\nsleep|30s\n", // holds the review open
	})
	o.Sup.Cfg.Repos["demo"] = repo
	startDispatch(t, o)
	j := &queue.Job{Title: "x", Goal: "g", Role: "developer", Repo: "demo"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	waitFor(t, 60*time.Second, "job in review", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateReview
	})
	// The developer may not deliver a verdict on its own job.
	got, _ := o.Sup.Jobs.Get(j.ID)
	if err := callVerdict(o, got.Assignee, j.ID, "merge", "self-approve"); err == nil {
		t.Fatal("developer verdict must be rejected")
	}
}

func callVerdict(o *office, agent string, jobID int64, verdict, notes string) error {
	return sockc.Call(o.Sup.SocketPath, agent, "job.verdict",
		proto.VerdictArgs{JobID: jobID, Verdict: verdict, Notes: notes}, nil)
}
