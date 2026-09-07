package supervisor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
	"github.com/scolastico-dev/one-man-office/internal/websupervisor/controlplane"
	"gopkg.in/yaml.v3"
)

func capacityControl(t *testing.T, o *office, limit int) *controlplane.Server {
	t.Helper()
	s := controlplane.New(limit, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	t.Cleanup(h.Close)
	attachControl(t, o, s, h.URL, "one")
	return s
}

func TestAggregateCapacityKeepsAINamingJobQueued(t *testing.T) {
	o := newOffice(t, map[string]string{"smokealarm": "ready\nbranchname|feat/preserved\nsleep|60s\n"})
	o.Sup.Cfg.Repos["demo"] = devRepo(t)
	o.Sup.Cfg.Branches.Naming = "ai"
	capacityControl(t, o, 1)
	lease, err := o.Sup.Control.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	j := &queue.Job{Title: "pending", Goal: "build", Role: "developer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.assign(j); !errors.Is(err, controlplane.ErrLimit) {
		t.Fatalf("capacity result: %v", err)
	}
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.State != queue.StateQueued || got.Retries != 0 {
		t.Fatalf("capacity failed job: %+v", got)
	}
	if err := o.Sup.Control.Release(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if branch, err := o.Sup.branchNameForJob(got); err != nil || branch != "feat/preserved" {
		t.Fatalf("retry branch=%q: %v", branch, err)
	}
}

func TestAggregateCapacityCompletesReviewWithOneProcessSlot(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{"developer": "ready\nshell|echo result > result.txt && git add result.txt && git commit -m feat\ndone|built\nwait\n", "reviewer": "ready\nverdict|merge|approved\ndone|merged\n"})
	o.Sup.Cfg.Repos["demo"] = repo
	control := capacityControl(t, o, 1)
	j := &queue.Job{Title: "one slot", Goal: "build", Role: "developer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	startDispatch(t, o)
	o.Sup.kickDispatch()
	waitFor(t, 8*time.Second, "single-slot review and merge", func() bool {
		used, _ := control.Stats()
		if used > 1 {
			t.Fatalf("process cap exceeded: %d", used)
		}
		var count int
		_ = o.DB.QueryRow("SELECT COUNT(*) FROM events WHERE kind = 'job_merged' AND job_id = ?", j.ID).Scan(&count)
		return count == 1
	})
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.Retries != 0 || got.State != queue.StateDone {
		t.Fatalf("capacity consumed retries: %+v", got)
	}
}

func TestAggregateCapacityReviewRejectionRestartsDeveloperInExistingWorktree(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, map[string]string{"developer": "ready\nshell|if test -e result.txt; then sleep 60; else echo result > result.txt && git add result.txt && git commit -m feat; fi\ndone|built\nwait\n", "reviewer": "ready\nverdict|reject|fix the result\nwait\n"})
	o.Sup.Cfg.Repos["demo"] = repo
	capacityControl(t, o, 1)
	j := &queue.Job{Title: "one slot", Goal: "build", Role: "developer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	startDispatch(t, o)
	o.Sup.kickDispatch()
	waitFor(t, 8*time.Second, "replacement developer after rejection", func() bool {
		var count int
		_ = o.DB.QueryRow("SELECT COUNT(*) FROM agents WHERE role = 'developer' AND job_id = ?", j.ID).Scan(&count)
		return count >= 2
	})
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.Retries != 0 || !strings.Contains(got.Note, "fix the result") {
		t.Fatalf("rework lost context: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(got.Worktree, "result.txt")); err != nil {
		t.Fatalf("worktree was lost: %v", err)
	}
}

func TestAggregateCapacityRetriesDeferredManagementRoles(t *testing.T) {
	for _, role := range []string{"ceo", "firefighter"} {
		t.Run(role, func(t *testing.T) {
			o := newOffice(t, map[string]string{role: "ready\nsleep|60s\n"})
			capacityControl(t, o, 1)
			lease, err := o.Sup.Control.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := o.Sup.spawnRole(role, 0, o.Dir, "continuity", 0); !errors.Is(err, controlplane.ErrLimit) {
				t.Fatalf("capacity result %v", err)
			}
			o.Sup.firefighterPaused = true
			for range 3 {
				o.Sup.dispatchOnce()
			}
			if o.Sup.ceoFailures != 0 {
				t.Fatal("capacity denial consumed CEO crash retries")
			}
			if err := o.Sup.Control.Release(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			o.Sup.dispatchOnce()
			living, err := db.LivingByRole(o.DB, role)
			if err != nil || len(living) != 1 {
				t.Fatalf("management spawn was stranded: %v, %v", living, err)
			}
		})
	}
}

func TestAggregateCapacityRetriesSmokeWithRoundTimeout(t *testing.T) {
	o := newOffice(t, map[string]string{"smokealarm": "ready\nsleep|60s\n"})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{Enabled: true, Mode: "all", Interval: config.Duration(time.Hour), Timeout: config.Duration(100 * time.Millisecond)}
	capacityControl(t, o, 1)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "freelancer-observed", Role: "freelancer", Profile: "freelancer"}); err != nil {
		t.Fatal(err)
	}
	lease, err := o.Sup.Control.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := o.Sup.runSmokeRound(); len(got) != 0 {
		t.Fatal("inspection bypassed capacity")
	}
	if err := o.Sup.Control.Release(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	o.Sup.dispatchOnce()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startDispatch(t, o)
	go o.Sup.SmokeLoop(ctx)
	waitFor(t, 3*time.Second, "deferred smoke round retains timeout", func() bool {
		var count int
		_ = o.DB.QueryRow("SELECT COUNT(*) FROM agents WHERE role = 'smokealarm'").Scan(&count)
		return count >= 2
	})
}

func TestSupervisedReloadRejectsChangedProviderOrCredentialScope(t *testing.T) {
	for _, change := range []string{"provider", "scope", "new-profile", "removed-profile"} {
		t.Run(change, func(t *testing.T) {
			o := newOffice(t, nil)
			p := o.Sup.Cfg.Models["developer"]
			p.Cmd = "claude"
			p.Provider = "claude"
			p.Env = map[string]string{"CLAUDE_CONFIG_DIR": "/registered"}
			o.Sup.Cfg.Models["developer"] = p
			capacityControl(t, o, 1)
			cfg := config.Defaults()
			cfg.Usage.Enabled = false
			cfg.Models = map[string]config.Profile{}
			for key, p := range o.Sup.Cfg.Models {
				cfg.Models[key] = p
			}
			cfg.Roles = map[string]config.RoleModels{}
			for role, models := range o.Sup.Cfg.Roles {
				cfg.Roles[role] = models
			}
			changed := cfg.Models["developer"]
			switch change {
			case "provider":
				changed.Provider = "codex"
			case "scope":
				changed.Env = map[string]string{"CLAUDE_CONFIG_DIR": "/other"}
			case "new-profile":
				cfg.Models["new"] = changed
			case "removed-profile":
				delete(cfg.Models, "developer")
				cfg.Roles["developer"] = cfg.Roles["ceo"]
			}
			if change != "removed-profile" {
				cfg.Models["developer"] = changed
			}
			raw, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(o.Dir, ".omo", "omo.yaml"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := sockc.Call(o.Sup.SocketPath, "user", "office.reload", nil, nil); err == nil || !strings.Contains(err.Error(), "restart") {
				t.Fatalf("unsafe supervised reload accepted: %v", err)
			}
			active := o.Sup.Config().Models["developer"]
			if active.Provider != "claude" || active.Env["CLAUDE_CONFIG_DIR"] != "/registered" {
				t.Fatal("rejected reload changed active config")
			}
			if err := o.Sup.Control.Ping(context.Background()); err != nil {
				t.Fatalf("rejected reload poisoned supervision: %v", err)
			}
		})
	}
}

func TestAggregateCapacityPreservesBranchWhenDeveloperIsDeferred(t *testing.T) {
	o := newOffice(t, map[string]string{"developer": "ready\nsleep|60s\n"})
	repo := devRepo(t)
	o.Sup.Cfg.Repos["demo"] = repo
	o.Sup.Cfg.Branches.Naming = "ai"
	capacityControl(t, o, 1)
	j := &queue.Job{Title: "resume", Goal: "work", Role: "developer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(o.Dir, ".omo", "worktrees", "demo-1")
	if err := o.Sup.Git.AddWorktree(repo, wt, "feat/preserved"); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetWorktree(j.ID, wt, "feat/preserved"); err != nil {
		t.Fatal(err)
	}
	j, _ = o.Sup.Jobs.Get(j.ID)
	lease, err := o.Sup.Control.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.assign(j); !errors.Is(err, controlplane.ErrLimit) {
		t.Fatalf("capacity result: %v", err)
	}
	if err := o.Sup.Control.Release(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	j, _ = o.Sup.Jobs.Get(j.ID)
	if j.State != queue.StateQueued || j.Branch != "feat/preserved" {
		t.Fatalf("lost branch: %+v", j)
	}
	if err := o.Sup.assign(j); err != nil {
		t.Fatal(err)
	}
	var namers int
	_ = o.DB.QueryRow("SELECT COUNT(*) FROM agents WHERE role = 'branch_namer'").Scan(&namers)
	if namers != 0 {
		t.Fatal("retry repeated AI naming despite existing worktree")
	}
}

func TestAggregateCapacityHandshakeReplacementRemainsRetryable(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "sleep|60s\n"})
	control := controlplane.New(1, nil, time.Minute)
	var deny atomic.Bool
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if deny.Load() && r.URL.Path == "/acquire" {
			http.Error(w, "capacity", http.StatusTooManyRequests)
			return
		}
		control.Handler().ServeHTTP(w, r)
	}))
	defer h.Close()
	attachControl(t, o, control, h.URL, "one")
	j := &queue.Job{Title: "handshake", Goal: "work", Role: "freelancer"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.assign(j); err != nil {
		t.Fatal(err)
	}
	deny.Store(true)
	waitFor(t, 5*time.Second, "handshake replacement queued for capacity", func() bool { got, _ := o.Sup.Jobs.Get(j.ID); return got.State == queue.StateQueued })
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.Assignee != "" {
		t.Fatalf("dead assignee retained: %+v", got)
	}
}

func TestAggregateCapacityDefersExplicitRestartOfJoblessSession(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "ready\nsleep|60s\n"})
	control := controlplane.New(1, nil, time.Minute)
	var deny atomic.Bool
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if deny.Load() && r.URL.Path == "/acquire" {
			http.Error(w, "capacity", http.StatusTooManyRequests)
			return
		}
		control.Handler().ServeHTTP(w, r)
	}))
	defer h.Close()
	attachControl(t, o, control, h.URL, "one")
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "keep working")
	if err != nil {
		t.Fatal(err)
	}
	deny.Store(true)
	if err := o.Sup.StopAgent(name, "user", "restart"); err != nil {
		t.Fatal(err)
	}
	deny.Store(false)
	o.Sup.dispatchOnce()
	living, err := db.LivingByRole(o.DB, "freelancer")
	if err != nil || len(living) != 1 || living[0].Name == name {
		t.Fatalf("jobless restart was lost: %+v, %v", living, err)
	}
}

func TestAggregateCapacityRetainsLastHandshakeAttemptAndFailoverProfile(t *testing.T) {
	for _, role := range []string{"freelancer", "reviewer", "branch_namer"} {
		t.Run(role, func(t *testing.T) {
			o := newOffice(t, map[string]string{"freelancer": "sleep|60s\n", "reviewer": "sleep|60s\n", "developer": "sleep|60s\n"})
			o.Sup.Cfg.Models["backup"] = o.Sup.Cfg.Models["freelancer"]
			for _, configuredRole := range []string{"freelancer", "reviewer", "developer"} {
				o.Sup.Cfg.Roles[configuredRole] = config.RoleModels{Models: []string{configuredRole, "backup"}, Assignment: config.AssignmentFailover}
			}
			o.Sup.Cfg.Roles["smokealarm"] = config.RoleModels{Models: []string{"smokealarm", "backup"}, Assignment: config.AssignmentFailover}
			o.Sup.Cfg.Repos["demo"] = devRepo(t)
			capacityControl(t, o, 1)
			lease, err := o.Sup.Control.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			jobRole := "freelancer"
			if role != "freelancer" {
				jobRole = "developer"
			}
			j := &queue.Job{Title: "last handshake", Goal: "work", Role: jobRole, Repo: "demo"}
			if err := o.Sup.Jobs.Create(j); err != nil {
				t.Fatal(err)
			}
			if role == "reviewer" {
				for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking, queue.StateReview} {
					if err := o.Sup.Jobs.Transition(j.ID, state); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Two real handshake failures preceded this lease request. The
			// deferred third attempt must still use the selected backup model.
			if _, err := o.Sup.spawnAttempt(role, "backup", j.ID, o.Dir, j.Goal, 2, true, false, false); !errors.Is(err, controlplane.ErrLimit) {
				t.Fatalf("capacity result: %v", err)
			}
			if err := o.Sup.Control.Release(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			var branchResult chan error
			switch role {
			case "freelancer":
				if err := o.Sup.assign(j); err != nil {
					t.Fatal(err)
				}
			case "reviewer":
				if err := o.Sup.spawnReviewer(j); err != nil {
					t.Fatal(err)
				}
			case "branch_namer":
				o.Sup.Cfg.Branches.Naming = "ai"
				branchResult = make(chan error, 1)
				go func() { _, err := o.Sup.branchNameForJob(j); branchResult <- err }()
			}
			waitFor(t, time.Second, "deferred attempt starts", func() bool { agents, _ := db.LivingByJobRole(o.DB, j.ID, role); return len(agents) > 0 })
			agents, _ := db.LivingByJobRole(o.DB, j.ID, role)
			if agents[0].Profile != "backup" {
				t.Fatalf("capacity retry forgot failover choice: %q", agents[0].Profile)
			}
			waitFor(t, 5*time.Second, "last real handshake failure exhausts retry budget", func() bool { got, _ := o.Sup.Jobs.Get(j.ID); return got.State == queue.StateFailed })
			var attempts int
			_ = o.DB.QueryRow("SELECT COUNT(*) FROM agents WHERE job_id = ?", j.ID).Scan(&attempts)
			if attempts != 1 {
				t.Fatalf("capacity reset handshake retry budget: %d new processes", attempts)
			}
			if branchResult != nil {
				select {
				case err := <-branchResult:
					if err == nil {
						t.Fatal("failed naming succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("failed naming waiter was stranded")
				}
			}
		})
	}
}

func TestDeferredConfiguredProfileRechecksCurrentEligibility(t *testing.T) {
	for _, change := range []string{"usage-capped", "role-model-removed"} {
		t.Run(change, func(t *testing.T) {
			o := newOffice(t, map[string]string{"freelancer": "ready\nsleep|60s\n"})
			backup := o.Sup.Cfg.Models["freelancer"]
			backup.Provider = "codex"
			o.Sup.Cfg.Models["backup"] = backup
			o.Sup.Cfg.Roles["freelancer"] = config.RoleModels{Models: []string{"freelancer", "backup"}, Assignment: config.AssignmentRoundRobin}
			o.Sup.Cfg.Usage = config.Usage{Enabled: true, SafeShutdownPercent: 85, WeeklyLimitPercent: 90}
			usage := &assignmentUsage{used: map[string]float64{"backup": 20}}
			o.Sup.Usage = usage
			capacityControl(t, o, 1)
			lease, err := o.Sup.Control.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			j := &queue.Job{Title: "waiting", Goal: "work", Role: "freelancer"}
			if err := o.Sup.Jobs.Create(j); err != nil {
				t.Fatal(err)
			}
			if _, err := o.Sup.spawnAttempt("freelancer", "backup", j.ID, o.Dir, j.Goal, 2, true, false, false); !errors.Is(err, controlplane.ErrLimit) {
				t.Fatalf("initial capacity: %v", err)
			}
			if change == "usage-capped" {
				usage.used["backup"] = 95
			} else {
				o.Sup.Cfg.Roles["freelancer"] = config.RoleModels{Models: []string{"freelancer"}, Assignment: config.AssignmentRoundRobin}
			}
			if err := o.Sup.Control.Release(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			if err := o.Sup.assign(j); err != nil {
				t.Fatal(err)
			}
			got, _ := o.Sup.Jobs.Get(j.ID)
			agent, err := db.GetAgent(o.DB, got.Assignee)
			if err != nil {
				t.Fatal(err)
			}
			if agent.Profile != "freelancer" {
				t.Fatalf("deferred spawn bypassed current eligibility: profile %q", agent.Profile)
			}
		})
	}
}

func TestExplicitJoblessRestartDefersHandshakeRetryWithoutLosingAttempt(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "sleep|60s\n"})
	control := controlplane.New(1, nil, time.Minute)
	var deny atomic.Bool
	var denied atomic.Int32
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if deny.Load() && r.URL.Path == "/acquire" {
			denied.Add(1)
			http.Error(w, "capacity", http.StatusTooManyRequests)
			return
		}
		control.Handler().ServeHTTP(w, r)
	}))
	defer h.Close()
	attachControl(t, o, control, h.URL, "one")
	name, err := o.Sup.spawnAttempt("freelancer", "freelancer", 0, o.Dir, "explicit restart", 1, true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	deny.Store(true)
	waitFor(t, 5*time.Second, "explicit restart handshake times out", func() bool {
		var count int
		_ = o.DB.QueryRow("SELECT COUNT(*) FROM events WHERE kind = 'handshake_timeout' AND agent = ?", name).Scan(&count)
		return count == 1
	})
	waitFor(t, time.Second, "timed out session releases lease", func() bool { used, _ := control.Stats(); return used == 0 })
	waitFor(t, time.Second, "handshake replacement is capacity denied", func() bool { return denied.Load() > 0 })
	deny.Store(false)
	startDispatch(t, o)
	o.Sup.kickDispatch()
	waitFor(t, 4*time.Second, "explicit restart resumes after capacity frees", func() bool {
		var count int
		_ = o.DB.QueryRow("SELECT COUNT(*) FROM agents WHERE role = 'freelancer'").Scan(&count)
		return count == 2
	})
	waitFor(t, 5*time.Second, "preserved last attempt exhausts handshake budget", func() bool {
		var count int
		_ = o.DB.QueryRow("SELECT COUNT(*) FROM events WHERE kind = 'spawn_failed'").Scan(&count)
		return count == 1
	})
	var count int
	_ = o.DB.QueryRow("SELECT COUNT(*) FROM agents WHERE role = 'freelancer'").Scan(&count)
	if count != 2 {
		t.Fatalf("handshake budget reset after capacity wait: %d processes", count)
	}
}

func TestDeferredJoblessSpawnsRemainPendingWhileQuotaBlocked(t *testing.T) {
	for _, role := range []string{"ceo", "smokealarm", "freelancer"} {
		t.Run(role, func(t *testing.T) {
			o := newOffice(t, map[string]string{role: "ready\nsleep|60s\n"})
			profile := o.Sup.Cfg.Models[role]
			profile.Provider = "codex"
			profile.Env = map[string]string{"CODEX_HOME": t.TempDir()}
			o.Sup.Cfg.Models[role] = profile
			spare := profile
			spare.Env = map[string]string{"CODEX_HOME": t.TempDir()}
			o.Sup.Cfg.Models["spare"] = spare
			o.Sup.Cfg.Roles["developer"] = config.RoleModels{Models: []string{"spare"}, Assignment: config.AssignmentRoundRobin}
			o.Sup.Cfg.Usage.Enabled = true
			o.Sup.Cfg.Usage.SafeShutdownPercent = 85
			o.Sup.Cfg.Usage.WeeklyLimitPercent = 90
			usage := &assignmentUsage{used: map[string]float64{role: 86, "spare": 20}}
			o.Sup.Usage = usage
			capacityControl(t, o, 1)
			request := capacitySpawn{role: role, profile: role, dir: o.Dir, goal: "pending", attempt: 2, configured: true, managementRestart: role == "freelancer"}
			if role == "freelancer" {
				o.Sup.queueExplicitRestart("old-agent", request)
			} else {
				o.Sup.deferManagementSpawn(request)
			}
			resume := func() {
				if role == "smokealarm" {
					o.Sup.runSmokeRound()
				} else {
					o.Sup.resumeCapacitySpawns()
				}
			}
			resume()
			var count int
			_ = o.DB.QueryRow("SELECT COUNT(*) FROM agents WHERE role = ?", role).Scan(&count)
			if count != 0 {
				t.Fatal("quota-blocked deferred profile launched")
			}
			usage.used[role] = 20
			resume()
			_ = o.DB.QueryRow("SELECT COUNT(*) FROM agents WHERE role = ?", role).Scan(&count)
			if count != 1 {
				t.Fatal("quota wait lost deferred request")
			}
		})
	}
}

func TestCapacityDeferredJobSurvivesRoleQuotaWaitBeforeSpawn(t *testing.T) {
	for _, mode := range []string{"configured", "explicit", "ai-naming"} {
		t.Run(mode, func(t *testing.T) {
			role := "freelancer"
			if mode == "ai-naming" {
				role = "developer"
			}
			profileRole := role
			scenarios := map[string]string{role: "ready\nbranchname|feat/quota-resume\nsleep|60s\n"}
			if mode == "ai-naming" {
				profileRole = "smokealarm"
				scenarios = map[string]string{"smokealarm": "ready\nbranchname|feat/quota-resume\nsleep|60s\n"}
			}
			o := newOffice(t, scenarios)
			// Exercise metered eligibility with the fake CLI, without adding
			// Codex-only trust flags or prompt arguments to that executable.
			disabled := false
			o.Sup.Cfg.TrustWorkdirs = &disabled
			profile := o.Sup.Cfg.Models[profileRole]
			profile.Provider = "codex"
			profile.InjectPrompt = &disabled
			profile.Env = map[string]string{"CODEX_HOME": t.TempDir()}
			o.Sup.Cfg.Models[profileRole] = profile
			spare := profile
			spare.Env = map[string]string{"CODEX_HOME": t.TempDir()}
			o.Sup.Cfg.Models["spare"] = spare
			o.Sup.Cfg.Roles["ceo"] = config.RoleModels{Models: []string{"spare"}, Assignment: config.AssignmentRoundRobin}
			o.Sup.Cfg.Usage = config.Usage{Enabled: true, SafeShutdownPercent: 85, WeeklyLimitPercent: 90}
			usage := &assignmentUsage{used: map[string]float64{profileRole: 20, "spare": 20}}
			o.Sup.Usage = usage
			capacityControl(t, o, 1)
			lease, err := o.Sup.Control.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			j := &queue.Job{Title: "quota wait", Goal: "work", Role: role}
			if mode == "explicit" {
				j.Model = role
			}
			if mode == "ai-naming" {
				o.Sup.Cfg.Repos["demo"] = devRepo(t)
				o.Sup.Cfg.Branches.Naming = "ai"
				j.Repo = "demo"
			}
			if err := o.Sup.Jobs.Create(j); err != nil {
				t.Fatal(err)
			}
			if err := o.Sup.assign(j); !errors.Is(err, controlplane.ErrLimit) {
				t.Fatalf("initial capacity wait: %v", err)
			}
			usage.used[profileRole] = 86
			if err := o.Sup.assign(j); !spawnBackpressure(err) {
				t.Fatalf("quota during capacity wait became terminal: %v", err)
			}
			got, _ := o.Sup.Jobs.Get(j.ID)
			if got.State != queue.StateQueued || got.Retries != 0 {
				t.Fatalf("quota wait changed durable work: %+v", got)
			}
			usage.used[profileRole] = 20
			if err := o.Sup.assign(got); !errors.Is(err, controlplane.ErrLimit) {
				t.Fatalf("eligible retry did not reach lease acquisition: %v", err)
			}
			if err := o.Sup.Control.Release(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			if mode == "ai-naming" {
				if branch, err := o.Sup.branchNameForJob(got); err != nil || branch != "feat/quota-resume" {
					t.Fatalf("quota-cleared naming retry: branch=%q err=%v", branch, err)
				}
			} else if err := o.Sup.assign(got); err != nil {
				t.Fatalf("quota-cleared job retry: %v", err)
			}
		})
	}
}

func TestSupervisedReloadAllowsArgumentsAndLimitsWithinRegisteredScope(t *testing.T) {
	o := newOffice(t, nil)
	capacityControl(t, o, 1)
	cfg := config.Defaults()
	cfg.Usage.Enabled = false
	cfg.Models = map[string]config.Profile{}
	for key, p := range o.Sup.Cfg.Models {
		cfg.Models[key] = p
	}
	cfg.Roles = o.Sup.Cfg.Roles
	p := cfg.Models["developer"]
	p.Args = []string{"new-argument"}
	cfg.Models["developer"] = p
	cfg.Limits.MaxDevelopers = 7
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Dir, ".omo", "omo.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "office.reload", nil, nil); err != nil {
		t.Fatal(err)
	}
	if o.Sup.Config().Limits.MaxDevelopers != 7 || o.Sup.Config().Models["developer"].Args[0] != "new-argument" {
		t.Fatal("compatible reload was not applied")
	}
}
