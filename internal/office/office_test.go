package office

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
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
	// CEO → PM job → developer job → review → merge → done.
	waitFor(t, 120*time.Second, "developer job merged", func() bool {
		return developerMergeFinished(o)
	})
	if _, err := os.Stat(filepath.Join(repo, "hello.txt")); err != nil {
		t.Fatal("merged feature missing on main")
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
	db.InsertAgent(o.DB, db.Agent{Name: "freelancer-ghost", Role: "freelancer", Profile: "x"})
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
	a, _ := db.GetAgent(o2.DB, "freelancer-ghost")
	if a.State != "dead" {
		t.Fatalf("stale agent not marked dead: %+v", a)
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
