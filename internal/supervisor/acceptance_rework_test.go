package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestScenarioCEOCreatesTopLevelDeveloperAndAutomerge(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{
		"ceo":       "ready\njobcreate|developer|top-level change|create the top-level artifact|api|\nwait\n",
		"developer": "ready\nshell|printf 'top-level\\n' > top-level.txt && git add top-level.txt && git commit -m top-level\ndone|built top-level artifact\nwait\n",
		"reviewer":  "ready\nverdict|merge|approved top-level\n",
	})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo}
	startDispatch(t, o)
	if _, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office"); err != nil {
		t.Fatal(err)
	}
	o.Sup.kickDispatch()
	var job *queue.Job
	waitFor(t, 60*time.Second, "CEO-created developer job", func() bool {
		jobs, err := o.Sup.Jobs.List()
		if err != nil {
			return false
		}
		for _, candidate := range jobs {
			if candidate.Role == "developer" && candidate.ParentJob == 0 {
				job = candidate
				return true
			}
		}
		return false
	})
	waitFor(t, 60*time.Second, "top-level developer job_merged", func() bool {
		var count int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'job_merged' AND job_id = ?`, job.ID).Scan(&count) == nil && count == 1
	})
	job, err := o.Sup.Jobs.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != queue.StateDone {
		t.Fatalf("top-level developer state = %s, want done", job.State)
	}
	if _, err := os.Stat(filepath.Join(repo, "top-level.txt")); err != nil {
		t.Fatalf("automerge artifact missing from checkout: %v", err)
	}
	if _, err := os.Stat(job.Worktree); !os.IsNotExist(err) {
		t.Fatalf("top-level worktree was not cleaned: %v", err)
	}
	if strings.TrimSpace(gitOutput(t, repo, "branch", "--list", job.Branch)) != "" {
		t.Fatalf("automerge branch %q was not deleted", job.Branch)
	}
	var doneID, mergedID int64
	if err := o.DB.QueryRow(`SELECT COALESCE(MAX(CASE WHEN kind = 'job_state' AND detail = 'merging→done' THEN id END), 0), COALESCE(MAX(CASE WHEN kind = 'job_merged' THEN id END), 0) FROM events WHERE job_id = ?`, job.ID).Scan(&doneID, &mergedID); err != nil {
		t.Fatal(err)
	}
	if doneID == 0 || mergedID == 0 || doneID >= mergedID {
		t.Fatalf("top-level completion event order done=%d merged=%d", doneID, mergedID)
	}
}

func TestScenarioPMAutomergeCleansMultiRepositoryIntegrations(t *testing.T) {
	api, web := devRepo(t), devRepo(t)
	o := newOffice(t, map[string]string{
		"ceo":             "ready\njobcreate|product_manager|multi-repo delivery|coordinate api and web||\nwait\n",
		"product_manager": "ready\njobcreate|developer|api change|build api|api|$JOB\njobcreate|developer|web change|build web|web|$JOB\nsleep|1h\n",
		"developer":       "ready\nshell|printf '%s\\n' \"$OMO_AGENT_ID\" > \"$OMO_AGENT_ID.txt\" && git add . && git commit -m child\ndone|built child\nwait\n",
		"reviewer":        "ready\nverdict|merge|approved child\n",
	})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: api}
	o.Sup.Cfg.Repos["web"] = config.Repository{Path: web}
	startDispatch(t, o)
	if _, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office"); err != nil {
		t.Fatal(err)
	}
	o.Sup.kickDispatch()
	var pm *queue.Job
	waitFor(t, 60*time.Second, "multi-repo PM job", func() bool {
		jobs, err := o.Sup.Jobs.List()
		if err != nil {
			return false
		}
		for _, candidate := range jobs {
			if candidate.Role == "product_manager" {
				pm = candidate
				return true
			}
		}
		return false
	})
	var children []*queue.Job
	waitFor(t, 60*time.Second, "multi-repo PM child jobs", func() bool {
		jobs, err := o.Sup.Jobs.List()
		if err != nil {
			return false
		}
		children = children[:0]
		for _, candidate := range jobs {
			if candidate.ParentJob == pm.ID && candidate.Role == "developer" {
				children = append(children, candidate)
			}
		}
		return len(children) == 2
	})
	waitFor(t, 60*time.Second, "multi-repo child merges", func() bool {
		for _, child := range children {
			var count int
			if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'job_merged' AND job_id = ?`, child.ID).Scan(&count); err != nil || count != 1 {
				return false
			}
		}
		return true
	})
	pm, err := o.Sup.Jobs.Get(pm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pm.IntegrationBranches) != 2 || pm.Assignee == "" {
		t.Fatalf("PM integration state = %#v, assignee=%q", pm.IntegrationBranches, pm.Assignee)
	}
	pmAgent := pm.Assignee
	if err := sockc.Call(o.Sup.SocketPath, pmAgent, "done", proto.DoneArgs{Result: "multi-repo delivered"}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "PM automerge job_merged", func() bool {
		var count int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'job_merged' AND job_id = ?`, pm.ID).Scan(&count) == nil && count == 1
	})
	pm, err = o.Sup.Jobs.Get(pm.ID)
	if err != nil || pm.State != queue.StateDone {
		t.Fatalf("PM completion = %#v, %v", pm, err)
	}
	for _, child := range children {
		child, err = o.Sup.Jobs.Get(child.ID)
		if err != nil {
			t.Fatal(err)
		}
		artifact := child.Assignee + ".txt"
		repo := api
		if child.Repo == "web" {
			repo = web
		}
		if _, err := os.Stat(filepath.Join(repo, artifact)); err != nil {
			t.Fatalf("PM automerge artifact %s missing from %s: %v", artifact, child.Repo, err)
		}
		integration := pm.IntegrationBranches[child.Repo]
		if _, err := os.Stat(integration.Worktree); !os.IsNotExist(err) {
			t.Fatalf("PM integration worktree for %s was not cleaned: %v", child.Repo, err)
		}
		if strings.TrimSpace(gitOutput(t, repo, "branch", "--list", integration.Branch)) != "" {
			t.Fatalf("PM integration branch for %s was not deleted", child.Repo)
		}
	}
	var doneID, mergedID int64
	if err := o.DB.QueryRow(`SELECT COALESCE(MAX(CASE WHEN kind = 'job_state' AND detail = 'merging→done' THEN id END), 0), COALESCE(MAX(CASE WHEN kind = 'job_merged' THEN id END), 0) FROM events WHERE job_id = ?`, pm.ID).Scan(&doneID, &mergedID); err != nil {
		t.Fatal(err)
	}
	if doneID == 0 || mergedID == 0 || doneID >= mergedID {
		t.Fatalf("PM completion event order done=%d merged=%d", doneID, mergedID)
	}
}

func TestScenarioConflictReconciliationAllowsSecondSocketVerdict(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{
		"ceo":             "ready\njobcreate|product_manager|conflict delivery|coordinate the retry||\nwait\n",
		"product_manager": "ready\njobcreate|developer|conflict child|reconcile shared.txt|api|$JOB\nsleep|1h\n",
		"developer":       "ready\nshell|printf 'developer v1\\n' > shared.txt && git add shared.txt && git commit -m first\ndone|first attempt\nwait\nshell|git merge omo/job-pm-1 -m reconcile || (printf 'developer v2\\n' > shared.txt && git add shared.txt && git commit -m reconcile)\ndone|reconciled\nwait\n",
		"reviewer":        "ready\nwait\n",
	})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo}
	startDispatch(t, o)
	if _, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office"); err != nil {
		t.Fatal(err)
	}
	o.Sup.kickDispatch()
	var pm *queue.Job
	waitFor(t, 60*time.Second, "conflict PM and child", func() bool {
		jobs, err := o.Sup.Jobs.List()
		if err != nil {
			return false
		}
		for _, candidate := range jobs {
			if candidate.Role == "product_manager" {
				pm = candidate
				return true
			}
		}
		return false
	})
	var job *queue.Job
	waitFor(t, 60*time.Second, "conflict child reaches first review", func() bool {
		jobs, err := o.Sup.Jobs.List()
		if err != nil {
			return false
		}
		for _, candidate := range jobs {
			if candidate.ParentJob == pm.ID && candidate.Role == "developer" {
				job = candidate
				return candidate.State == queue.StateReview
			}
		}
		return false
	})
	pm, err := o.Sup.Jobs.Get(pm.ID)
	if err != nil {
		t.Fatal(err)
	}
	integration := pm.IntegrationBranches["api"]
	if err := os.WriteFile(filepath.Join(integration.Worktree, "shared.txt"), []byte("integration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, integration.Worktree, "add", "shared.txt")
	gitOutput(t, integration.Worktree, "commit", "-m", "integration change")
	var reviewerName string
	waitFor(t, 60*time.Second, "assigned reviewer", func() bool {
		agents, err := db.LivingByRole(o.DB, "reviewer")
		if err != nil {
			return false
		}
		for _, agent := range agents {
			if agent.JobID == job.ID {
				reviewerName = agent.Name
				return true
			}
		}
		return false
	})
	firstErr := sockc.Call(o.Sup.SocketPath, reviewerName, "job.verdict", proto.VerdictArgs{JobID: job.ID, Verdict: "merge", Notes: "first merge"}, nil)
	if firstErr == nil || !strings.Contains(firstErr.Error(), "merge conflict") {
		t.Fatalf("first verdict error = %v, want merge conflict", firstErr)
	}
	waitFor(t, 60*time.Second, "durable merge conflict", func() bool {
		var conflicts int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'job_merge_conflict' AND job_id = ?`, job.ID).Scan(&conflicts) == nil && conflicts == 1
	})
	o.Sup.WakeAgent(job.Assignee)
	waitFor(t, 60*time.Second, "developer returns for reconciliation", func() bool {
		got, err := o.Sup.Jobs.Get(job.ID)
		var reviews int
		if queryErr := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'review_started' AND job_id = ?`, job.ID).Scan(&reviews); queryErr != nil {
			return false
		}
		return err == nil && got.State == queue.StateReview && reviews >= 2
	})
	waitFor(t, 60*time.Second, "fresh reviewer", func() bool {
		agents, err := db.LivingByRole(o.DB, "reviewer")
		if err != nil {
			return false
		}
		for _, agent := range agents {
			if agent.JobID == job.ID {
				reviewerName = agent.Name
				return true
			}
		}
		return false
	})
	if err := sockc.Call(o.Sup.SocketPath, reviewerName, "job.verdict", proto.VerdictArgs{JobID: job.ID, Verdict: "merge", Notes: "reconciled"}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "second verdict job_merged", func() bool {
		var count int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'job_merged' AND job_id = ?`, job.ID).Scan(&count) == nil && count == 1
	})
	if got, err := os.ReadFile(filepath.Join(integration.Worktree, "shared.txt")); err != nil || string(got) != "developer v2\n" {
		t.Fatalf("reconciled PM integration = %q, err=%v", got, err)
	}
}

func TestScenarioAsIsPreservesGlobalAndRepositoryCheckoutState(t *testing.T) {
	cases := []struct {
		name   string
		global string
		repo   string
	}{
		{name: "global asis", global: config.MergeTargetAsIs},
		{name: "repository override asis", global: config.MergeTargetAutoMerge, repo: config.MergeTargetAsIs},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			repo := devRepo(t)
			if err := os.WriteFile(filepath.Join(repo, "dirty-staged.txt"), []byte("staged\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitOutput(t, repo, "add", "dirty-staged.txt")
			if err := os.WriteFile(filepath.Join(repo, "dirty-untracked.txt"), []byte("untracked\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			beforeBranch := strings.TrimSpace(gitOutput(t, repo, "branch", "--show-current"))
			beforeStatus := gitOutput(t, repo, "status", "--porcelain")
			o := newOffice(t, map[string]string{
				"ceo":       "ready\njobcreate|developer|asis change|leave a pull request branch|api|\nsleep|1h\n",
				"developer": "ready\nshell|printf 'asis artifact\\n' > asis-artifact.txt && git add asis-artifact.txt && git commit -m asis\ndone|left branch for review\nwait\n",
				"reviewer":  "ready\nverdict|merge|approved as-is\n",
			})
			o.Sup.Cfg.Branches.MergeTarget = test.global
			o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo, MergeTarget: test.repo}
			startDispatch(t, o)
			if _, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office"); err != nil {
				t.Fatal(err)
			}
			o.Sup.kickDispatch()
			var job *queue.Job
			waitFor(t, 60*time.Second, "as-is developer job", func() bool {
				jobs, err := o.Sup.Jobs.List()
				if err != nil {
					return false
				}
				for _, candidate := range jobs {
					if candidate.Role == "developer" {
						job = candidate
						return true
					}
				}
				return false
			})
			waitFor(t, 60*time.Second, "as-is job_merged", func() bool {
				var count int
				return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'job_merged' AND job_id = ?`, job.ID).Scan(&count) == nil && count == 1
			})
			var err error
			job, err = o.Sup.Jobs.Get(job.ID)
			if err != nil || job.State != queue.StateDone {
				t.Fatalf("as-is completion = %#v, %v", job, err)
			}
			if job.Branch == "" || job.Worktree == "" {
				t.Fatalf("as-is completion lost assigned branch/worktree: %#v", job)
			}
			if got := strings.TrimSpace(gitOutput(t, repo, "branch", "--show-current")); got != beforeBranch {
				t.Fatalf("checkout branch changed from %q to %q", beforeBranch, got)
			}
			if got := gitOutput(t, repo, "status", "--porcelain"); got != beforeStatus {
				t.Fatalf("dirty checkout changed from %q to %q", beforeStatus, got)
			}
			if _, err := os.Stat(filepath.Join(repo, "asis-artifact.txt")); !os.IsNotExist(err) {
				t.Fatalf("as-is artifact changed checkout: %v", err)
			}
			if err := gitCheckRef(t, repo, job.Branch); err != nil {
				t.Fatalf("as-is branch was not retained: %v", err)
			}
			if _, err := os.Stat(job.Worktree); !os.IsNotExist(err) {
				t.Fatalf("as-is worktree was not cleaned: %v", err)
			}
			for _, recipient := range []string{"user", o.Sup.CEOName()} {
				mail, err := o.Sup.Mail.Inbox(recipient)
				if err != nil || len(mail) != 1 || !strings.Contains(mail[0].Body, job.Branch) || !strings.Contains(mail[0].Body, "pull request") {
					t.Fatalf("%s as-is mail = %#v, %v", recipient, mail, err)
				}
			}
		})
	}
}

func gitCheckRef(t *testing.T, repo, branch string) error {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "show-ref", "--verify", "refs/heads/"+branch)
	return cmd.Run()
}

func TestScenarioPMCheckoutConflictRetriesDoneAndCleansIntegration(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{
		"ceo":             "ready\njobcreate|product_manager|checkout conflict|coordinate integration||\nsleep|1h\n",
		"product_manager": "ready\njobcreate|developer|integration change|build artifact|api|$JOB\nsleep|1h\n",
		"developer":       "ready\nshell|printf 'integration\\n' > shared.txt && git add shared.txt && git commit -m child\ndone|child merged\nwait\n",
		"reviewer":        "ready\nverdict|merge|approved child\n",
	})
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo}
	startDispatch(t, o)
	if _, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office"); err != nil {
		t.Fatal(err)
	}
	o.Sup.kickDispatch()
	var pm *queue.Job
	waitFor(t, 60*time.Second, "PM checkout-conflict job", func() bool {
		jobs, err := o.Sup.Jobs.List()
		if err != nil {
			return false
		}
		for _, candidate := range jobs {
			if candidate.Role == "product_manager" {
				pm = candidate
				return true
			}
		}
		return false
	})
	var child *queue.Job
	waitFor(t, 60*time.Second, "PM checkout-conflict child merge", func() bool {
		jobs, err := o.Sup.Jobs.List()
		if err != nil {
			return false
		}
		for _, candidate := range jobs {
			if candidate.ParentJob == pm.ID && candidate.Role == "developer" {
				child = candidate
				var merged int
				return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'job_merged' AND job_id = ?`, child.ID).Scan(&merged) == nil && merged == 1
			}
		}
		return false
	})
	pm, err := o.Sup.Jobs.Get(pm.ID)
	if err != nil {
		t.Fatal(err)
	}
	integration := pm.IntegrationBranches["api"]
	if integration.Worktree == "" || pm.Assignee == "" {
		t.Fatalf("PM integration before retry = %#v, assignee=%q", integration, pm.Assignee)
	}
	if err := os.WriteFile(filepath.Join(repo, "shared.txt"), []byte("checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, repo, "add", "shared.txt")
	gitOutput(t, repo, "commit", "-m", "checkout conflict")
	firstErr := sockc.Call(o.Sup.SocketPath, pm.Assignee, "done", proto.DoneArgs{Result: "first PM completion"}, nil)
	if firstErr == nil || !strings.Contains(firstErr.Error(), "merge conflict") {
		t.Fatalf("first PM done error = %v, want merge conflict", firstErr)
	}
	waitFor(t, 60*time.Second, "PM returns to working after checkout conflict", func() bool {
		got, err := o.Sup.Jobs.Get(pm.ID)
		return err == nil && got.State == queue.StateWorking && strings.Contains(got.Note, "api")
	})
	if _, err := os.Stat(integration.Worktree); err != nil {
		t.Fatalf("integration worktree removed after PM conflict: %v", err)
	}
	if err := gitCheckRef(t, repo, integration.Branch); err != nil {
		t.Fatalf("integration branch removed after PM conflict: %v", err)
	}
	gitOutput(t, repo, "rm", "shared.txt")
	gitOutput(t, repo, "commit", "-m", "reconcile checkout")
	if err := sockc.Call(o.Sup.SocketPath, pm.Assignee, "done", proto.DoneArgs{Result: "second PM completion"}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "PM retry job_merged", func() bool {
		var merged int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'job_merged' AND job_id = ?`, pm.ID).Scan(&merged) == nil && merged == 1
	})
	if got, err := os.ReadFile(filepath.Join(repo, "shared.txt")); err != nil || string(got) != "integration\n" {
		t.Fatalf("reconciled PM checkout = %q, err=%v", got, err)
	}
	if _, err := os.Stat(integration.Worktree); !os.IsNotExist(err) {
		t.Fatalf("integration worktree was not cleaned after retry: %v", err)
	}
	if err := gitCheckRef(t, repo, integration.Branch); err == nil {
		t.Fatal("integration branch was not cleaned after retry")
	}
}
