package office

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/supervisor"
)

func TestMain(m *testing.M) {
	// Build the real omo binary: mock profiles must NOT resolve to the test
	// binary via os.Executable() (that would recursively run the tests).
	tmp, err := os.MkdirTemp("", "omo-bin-*")
	if err != nil {
		panic(err)
	}
	bin := filepath.Join(tmp, "omo")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/omo")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic(string(out))
	}
	MockSelf = bin
	if err := os.Setenv("OMO_HOME", filepath.Join(tmp, "home")); err != nil {
		panic(err)
	}

	supervisor.StartPromptDelay = 10 * time.Millisecond
	supervisor.ReadyTimeout = 10 * time.Second
	supervisor.GraceBeforeReap = 50 * time.Millisecond
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	os.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	os.Setenv("GIT_AUTHOR_NAME", "omo-test")
	os.Setenv("GIT_AUTHOR_EMAIL", "omo@test")
	os.Setenv("GIT_COMMITTER_NAME", "omo-test")
	os.Setenv("GIT_COMMITTER_EMAIL", "omo@test")
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

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

func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", desc)
}

func developerMergeFinished(o *Office) bool {
	jobs, _ := o.Sup.Jobs.List(queue.StateDone)
	events, _ := db.EventsSince(o.DB, 0)
	for _, j := range jobs {
		if j.Role != "developer" {
			continue
		}
		for _, event := range events {
			if event.Kind == "job_merged" && event.JobID == j.ID {
				return true
			}
		}
	}
	return false
}

func developerMergePath(o *Office, fallback string) string {
	jobs, _ := o.Sup.Jobs.List(queue.StateDone)
	events, _ := db.EventsSince(o.DB, 0)
	for _, j := range jobs {
		if j.Role != "developer" || j.ParentJob == 0 {
			continue
		}
		for _, event := range events {
			if event.Kind != "job_merged" || event.JobID != j.ID {
				continue
			}
			pm, err := o.Sup.Jobs.Get(j.ParentJob)
			if err != nil {
				continue
			}
			if integration, ok := pm.IntegrationBranches[j.Repo]; ok && integration.Worktree != "" {
				return integration.Worktree
			}
		}
	}
	return fallback
}

// mockOffice writes an omo.yaml (repo "demo" → a fresh git repo) and opens
// a mock office. The mock profiles run `omo fake-agent --auto-role <role>`.
func mockOffice(t *testing.T) (*Office, string) {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "demo-repo")
	os.MkdirAll(repo, 0o755)
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("demo\n"), 0o644)
	gitInit(t, repo)
	yaml := `
repos:
  demo: ` + repo + `
models:
  any:
    cmd: claude
roles:
  ceo: any
  product_manager: any
  developer: any
  reviewer: any
  freelancer: any
  smokealarm: any
  firefighter: any
smokealarm:
  interval: 1h
`
	os.MkdirAll(filepath.Join(dir, ".omo"), 0o755)
	os.WriteFile(filepath.Join(dir, ConfigPath), []byte(yaml), 0o644)
	o, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(o.Close)
	return o, repo
}

func TestMockOfficeRunsFullOrgChart(t *testing.T) {
	o, repo := mockOffice(t)
	if err := o.Start(); err != nil {
		t.Fatal(err)
	}
	// CEO → PM job → developer job → review → merge → done. The child lands
	// in the PM integration worktree while the mock PM remains waiting.
	waitFor(t, 120*time.Second, "developer job merged", func() bool {
		return developerMergeFinished(o)
	})
	if _, err := os.Stat(filepath.Join(developerMergePath(o, repo), "hello.txt")); err != nil {
		t.Fatal("merged feature missing from the PM integration target")
	}
	// Event trail exists.
	evs, _ := db.EventsSince(o.DB, 0)
	kinds := map[string]bool{}
	for _, e := range evs {
		kinds[e.Kind] = true
	}
	for _, want := range []string{"agent_spawned", "agent_ready", "job_created", "job_state", "job_merged"} {
		if !kinds[want] {
			t.Errorf("missing event kind %q", want)
		}
	}
}

func TestOpenReadOnlyCanObserveOwnedOfficeWithoutSideEffects(t *testing.T) {
	dir := t.TempDir()
	yaml := `
repos: {}
models:
  any:
    cmd: claude
roles:
  ceo: any
  product_manager: any
  developer: any
  reviewer: any
  freelancer: any
  smokealarm: any
  firefighter: any
`
	if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigPath), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	ownerDB, err := db.Open(filepath.Join(dir, ".omo", "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ownerDB.Close()
	lock, err := claimInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	before, err := db.AllEvents(ownerDB)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !observer.ReadOnly || observer.Srv != nil || observer.instanceLock != nil {
		t.Fatalf("observer acquired mutable runtime state: %+v", observer)
	}
	observer.Close()
	after, err := db.AllEvents(ownerDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("read-only open changed events: before=%d after=%d", len(before), len(after))
	}
	if err := db.AppendEvent(ownerDB, "owner_still_writable", "", 0, "ok"); err != nil {
		t.Fatalf("observer close affected owner: %v", err)

	}
}

func TestOpenFailsUsagePreflightBeforeRuntimeState(t *testing.T) {
	dir := t.TempDir()
	credentialRoot := filepath.Join(dir, "missing-codex-home")
	yaml := `
repos: {}
models:
  metered:
    cmd: codex
    env:
      CODEX_HOME: ` + credentialRoot + `
roles:
  ceo: metered
  product_manager: metered
  developer: metered
  reviewer: metered
  freelancer: metered
  smokealarm: metered
  firefighter: metered
`
	if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigPath), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, false); err == nil || !strings.Contains(err.Error(), "usage preflight") {
		t.Fatalf("startup preflight error = %v", err)
	}
	for _, forbidden := range []string{filepath.Join(dir, ".omo", "omo.db"), filepath.Join(dir, LockPath)} {
		if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
			t.Fatalf("preflight failure created %s", forbidden)
		}
	}
}

func TestSafeModeStartsOnlyCEOAndResumeBootsOffice(t *testing.T) {
	o, _ := mockOffice(t)
	o.SafeMode = true
	if err := o.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "safe-mode CEO ready", func() bool {
		agents, _ := db.LivingByRole(o.DB, "ceo")
		return len(agents) == 1 && agents[0].State == "working"
	})
	time.Sleep(1200 * time.Millisecond)
	agents, err := db.LivingAgents(o.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Role != "ceo" {
		t.Fatalf("safe-mode living agents = %+v, want CEO only", agents)
	}
	if !strings.Contains(agents[0].Goal, "SAFE MODE IS ACTIVE") {
		t.Fatalf("CEO goal missing safe-mode context:\n%s", agents[0].Goal)
	}

	o.Sup.ResumeSpawning("user")
	waitFor(t, 15*time.Second, "product manager after resume", func() bool {
		agents, _ := db.LivingByRole(o.DB, "product_manager")
		return len(agents) > 0
	})
}

func TestCloseWaitsForRuntimeLoops(t *testing.T) {
	o, _ := mockOffice(t)
	started := make(chan struct{})
	release := make(chan struct{})
	o.startRuntime(func() {
		close(started)
		<-release
	})
	<-started

	closed := make(chan struct{})
	go func() {
		o.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a runtime loop was still active")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the runtime loop stopped")
	}
}

func TestCloseCancelsManualPluginAndPersistsOutcomeBeforeClosingDatabase(t *testing.T) {
	o, _ := mockOffice(t)
	dir := filepath.Join(o.Dir, plugins.Dir, "blocking")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"blocking","hooks":[{"event":"manual","name":"run","description":"Run action","lua":"hook.lua","timeout":"2s"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`omo.local_set("entered", true); while true do end`), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	o.Sup.Plugins, err = plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- o.Sup.TriggerPlugin("user", "blocking", "run", nil) }()
	waitFor(t, time.Second, "manual hook entered", func() bool {
		var count int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE plugin='blocking' AND key='entered'`).Scan(&count) == nil && count == 1
	})
	closed := make(chan struct{})
	go func() { o.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Error("office shutdown did not cancel the running manual hook")
		<-closed
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "database is closed") {
		t.Errorf("manual cancellation = %v", err)
	}
	if err := o.Sup.TriggerPlugin("user", "blocking", "run", nil); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Errorf("post-close trigger = %v", err)
	}
	database, err := db.OpenReadOnly(filepath.Join(o.Dir, ".omo", "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_failed'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("durable failure outcomes = %d, want one", count)
	}
}

func TestRestartRecoveryRequeuesNonTerminalJobs(t *testing.T) {
	o, _ := mockOffice(t)
	// Simulate a previous run's leftovers directly in the DB.
	jobs := o.Sup.Jobs
	j1 := &queue.Job{Title: "was-working", Goal: "g", Role: "freelancer"}
	jobs.Create(j1)
	jobs.Transition(j1.ID, queue.StateAssigned)
	jobs.Transition(j1.ID, queue.StateWorking)
	jobs.SetAssignee(j1.ID, "freelancer-ghost")
	j2 := &queue.Job{Title: "was-done", Goal: "g", Role: "freelancer"}
	jobs.Create(j2)
	jobs.Transition(j2.ID, queue.StateAssigned)
	jobs.Transition(j2.ID, queue.StateWorking)
	jobs.Transition(j2.ID, queue.StateMerging)
	jobs.Transition(j2.ID, queue.StateDone)
	j3 := &queue.Job{Title: "was-in-review", Goal: "g", Role: "developer", Repo: "demo"}
	jobs.Create(j3)
	jobs.Transition(j3.ID, queue.StateAssigned)
	jobs.Transition(j3.ID, queue.StateWorking)
	jobs.Transition(j3.ID, queue.StateReview)
	jobs.SetAssignee(j3.ID, "developer-review-ghost")
	db.InsertAgent(o.DB, db.Agent{Name: "freelancer-ghost", Role: "freelancer", Profile: "x"})
	db.InsertAgent(o.DB, db.Agent{Name: "developer-review-ghost", Role: "developer", Profile: "x", JobID: j3.ID})
	if _, err := o.DB.Exec(`INSERT INTO incidents (agent, class, detail) VALUES ('freelancer-ghost', 'stuck', 'old run')`); err != nil {
		t.Fatal(err)
	}
	o.Close()

	// Reopen the same office dir: recovery runs in Open.
	o2, err := Open(o.Dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer o2.Close()
	g1, _ := o2.Sup.Jobs.Get(j1.ID)
	if g1.State != queue.StateQueued || g1.Note != o2.Sup.Msgs.RestartNote() || g1.Assignee != "" {
		t.Fatalf("j1 not recovered: %+v", g1)
	}
	g2, _ := o2.Sup.Jobs.Get(j2.ID)
	if g2.State != queue.StateDone {
		t.Fatalf("terminal job touched: %+v", g2)
	}
	g3, _ := o2.Sup.Jobs.Get(j3.ID)
	if g3.State != queue.StateQueued || g3.Note != o2.Sup.Msgs.RestartReviewNote() || g3.Assignee != "" {
		t.Fatalf("reviewing job lost restart-review context: %+v", g3)
	}
	for _, want := range []string{"already entered review", "self-check", "omo done"} {
		if !strings.Contains(g3.Note, want) {
			t.Errorf("review restart note missing %q: %s", want, g3.Note)
		}
	}
	a, _ := db.GetAgent(o2.DB, "freelancer-ghost")
	if a.State != "dead" {
		t.Fatalf("stale agent not marked dead: %+v", a)
	}
	var open int
	if err := o2.DB.QueryRow(`SELECT COUNT(*) FROM incidents WHERE state = 'open'`).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 0 {
		t.Fatalf("open incidents after restart = %d", open)
	}
	var closedEvent int
	if err := o2.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'incidents_closed_on_restart'`).Scan(&closedEvent); err != nil {
		t.Fatal(err)
	}
	if closedEvent != 1 {
		t.Fatalf("restart incident-close events = %d", closedEvent)
	}
}

func TestClosePersistsOverallStatistics(t *testing.T) {
	o, _ := mockOffice(t)
	if _, err := o.DB.Exec(`INSERT INTO agents
		(name, role, profile, state, created_at, ended_at)
		VALUES ('developer-stats', 'developer', 'archive-model', 'done', '2026-01-01 00:00:00', '2026-01-01 00:00:03')`); err != nil {
		t.Fatal(err)
	}
	for _, event := range []struct{ kind, at string }{
		{"agent_ready", "2026-01-01 00:00:01"},
		{"agent_done", "2026-01-01 00:00:03"},
	} {
		if _, err := o.DB.Exec(`INSERT INTO events (kind, agent, created_at) VALUES (?, 'developer-stats', ?)`, event.kind, event.at); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(o.Dir, ".omo", "omo.db")
	o.Close()
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := db.OverallStatistics(d)
	if err != nil || len(rows) != 1 || rows[0].Model != "archive-model" || rows[0].Active != 2*time.Second {
		t.Fatalf("shutdown statistics = %+v, %v", rows, err)
	}
}

func TestSmokeAlarmToFirefighterPath(t *testing.T) {
	// Custom scenarios: a hanging freelancer, an alarm that reports it, a
	// firefighter that kills it and resolves.
	o, _ := mockOffice(t)
	dir := o.Dir
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		return p
	}
	self := o.Cfg.Models["mock-freelancer"].Cmd // the omo binary path
	scenario := func(file string) []string { return []string{"fake-agent", "--scenario", file} }
	setProfile := func(role, content string) {
		p := o.Cfg.Models["mock-"+role]
		p.Cmd = self
		p.Args = scenario(write(role+".scenario", content))
		o.Cfg.Models["mock-"+role] = p
	}
	setProfile("ceo", "ready\nsleep|1h\n") // a real CEO idles at its prompt, it never waits
	setProfile("freelancer", "ready\nhang\n")
	setProfile("smokealarm", "ready\nincident|freelancer|stuck|hanging\ndone|round complete: 1 incidents\n")
	setProfile("firefighter", "ready\nagentkill|freelancer\nresolvefirst|killed the hung freelancer\ndone|resolved\n")
	o.Cfg.SmokeAlarm.Interval = mustDuration("1s")
	if err := o.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.Spawn("freelancer", "mock-freelancer", 0, o.Dir, "hang"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 120*time.Second, "incident resolved and user informed", func() bool {
		var n int
		o.DB.QueryRow(`SELECT COUNT(*) FROM incidents WHERE state = 'resolved'`).Scan(&n)
		if n == 0 {
			return false
		}
		u, _ := o.Sup.Mail.UnreadCount("user")
		return u > 0
	})
}

func mustDuration(s string) config.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return config.Duration(d)
}
