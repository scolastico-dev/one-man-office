package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestSmokeReportContainsAgentTailAndChatter(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|30s\n",
	})
	name, _ := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "long research goal")
	waitFor(t, 5*time.Second, "agent up", func() bool { return agentState(t, o, name) == "working" })
	report := o.Sup.smokeReport()
	for _, want := range []string{
		name,
		"freelancer",
		"long research goal",
		"SNAPSHOT OBSERVED AT:",
		"SESSION OUTPUT UPDATED AT:",
		"OUTPUT CHANGED SINCE PRIOR: unknown",
		"PM lateral messages since last round",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	// Second round: events since round one are included, not repeated forever.
	db.AppendEvent(o.DB, "test_marker", "", 0, "round-two-marker")
	report2 := o.Sup.smokeReport()
	if !strings.Contains(report2, "round-two-marker") {
		t.Error("second report missing new event")
	}
	if !strings.Contains(report2, "OUTPUT FROM 1 SMOKE RUN(S) AGO") {
		t.Error("second report missing prior-run output for comparison")
	}
	if !strings.Contains(report2, "OUTPUT CHANGED SINCE PRIOR:") || strings.Contains(report2, "OUTPUT CHANGED SINCE PRIOR: unknown") {
		t.Error("second report did not compare current output with the prior snapshot")
	}
	report3 := o.Sup.smokeReport()
	if strings.Contains(report3, "round-two-marker") {
		t.Error("third report repeats old events — delta tracking broken")
	}
}

func TestSmokeReportExplainsInteractiveCEOState(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo-ada", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "ceo-ada", "working"); err != nil {
		t.Fatal(err)
	}
	report := o.Sup.smokeReport()
	if !strings.Contains(report, "CEO INTERPRETATION:") || !strings.Contains(report, "idle prompt is expected") {
		t.Fatalf("CEO report lacks role-specific liveness guidance:\n%s", report)
	}
}

func TestSmokeReportExplainsWaitingAgentAndJobState(t *testing.T) {
	o := newOffice(t, nil)
	j := &queue.Job{Title: "implement scanner", Goal: "finish the scanner", Role: "developer", Repo: "scanner"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking, queue.StateReview} {
		if err := o.Sup.Jobs.Transition(j.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	const name = "developer-waiting"
	if err := o.Sup.Jobs.SetAssignee(j.ID, name); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "developer", Profile: "developer", JobID: j.ID, Goal: j.Goal}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, name, "waiting"); err != nil {
		t.Fatal(err)
	}

	report := o.Sup.smokeReport()
	for _, want := range []string{
		"AGENT STATE: waiting",
		"JOB: #1 state=review role=developer",
		"UNREAD MAIL: 0",
		"PARKED IN `omo wait`",
		"Quiet or unchanged output is expected",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
}

func TestSmokeLoopSpawnsFreshAlarmAndIncidentSpawnsFirefighter(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer":  "ready\nhang\n",
		"smokealarm":  "ready\nincident|freelancer|stuck|no output for a while\ndone|round complete: 1 incidents\n",
		"firefighter": "ready\nsleep|30s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, Mode: "all", Interval: config.Duration(500 * time.Millisecond), TailLines: 20,
		HistoryRuns: 2, IncludeEvents: true, IncludePMChatter: true,
	}
	name, _ := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "hang forever")
	waitFor(t, 5*time.Second, "victim up", func() bool { return agentState(t, o, name) == "working" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	// A smoke alarm spawns, files an incident, exits; a firefighter spawns.
	waitFor(t, 30*time.Second, "open incident recorded", func() bool {
		var n int
		o.DB.QueryRow(`SELECT COUNT(*) FROM incidents WHERE state = 'open'`).Scan(&n)
		return n >= 1
	})
	waitFor(t, 30*time.Second, "firefighter living", func() bool {
		n, _ := db.CountLivingByRole(o.DB, "firefighter")
		return n == 1
	})
	// The firefighter's goal names the incident id.
	ff := onlyAgentOfRole(t, o, "firefighter")
	a, _ := db.GetAgent(o.DB, ff)
	if !strings.Contains(a.Goal, "INCIDENT_ID: ") {
		t.Fatalf("firefighter goal missing INCIDENT_ID line:\n%s", a.Goal)
	}
}

func TestSmokeAlarmCanRaiseOnlyOneIncidentPerRun(t *testing.T) {
	o := newOffice(t, map[string]string{
		"smokealarm":  "ready\nsleep|60s\n",
		"firefighter": "ready\nsleep|60s\n",
	})
	smoke, err := o.Sup.Spawn("smokealarm", "smokealarm", 0, o.Dir, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "smoke alarm up", func() bool { return agentState(t, o, smoke) == "working" })
	args := proto.IncidentCreateArgs{Agent: "developer-x", Class: "stuck", Detail: "no progress"}
	if err := sockc.Call(o.Sup.SocketPath, smoke, "incident.create", args, nil); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, smoke, "incident.create", args, nil); err == nil {
		t.Fatal("same smoke alarm raised a second incident")
	}
	var open int
	o.DB.QueryRow(`SELECT COUNT(*) FROM incidents WHERE state = 'open'`).Scan(&open)
	if open != 1 {
		t.Fatalf("open incidents = %d, want 1", open)
	}
}

func TestSmokeLoopPausesForFirefighterThenResumes(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer":  "ready\nsleep|60s\n",
		"smokealarm":  "ready\nsleep|60s\n",
		"firefighter": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "all", Interval: config.Duration(100 * time.Millisecond), TailLines: 20,
	}
	victim, _ := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	waitFor(t, 5*time.Second, "victim up", func() bool { return agentState(t, o, victim) == "working" })
	ff, _ := o.Sup.Spawn("firefighter", "firefighter", 0, o.Dir, "repair")
	waitFor(t, 5*time.Second, "firefighter up", func() bool { return agentState(t, o, ff) == "working" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	time.Sleep(350 * time.Millisecond)
	if n, _ := db.CountLivingByRole(o.DB, "smokealarm"); n != 0 {
		t.Fatalf("smoke alarms running with firefighter: %d", n)
	}
	if err := o.Sup.KillAgent(ff, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "smoke alarm after firefighter", func() bool {
		n, _ := db.CountLivingByRole(o.DB, "smokealarm")
		return n == 1
	})
}

func TestPerAgentSmokeModeDefersAlarmsDuringSpawnHalt(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "per_agent", Interval: config.Duration(100 * time.Millisecond), TailLines: 20,
	}
	for i := 0; i < 2; i++ {
		name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, 5*time.Second, "freelancer up", func() bool { return agentState(t, o, name) == "working" })
	}
	o.Sup.mu.Lock()
	o.Sup.ceoSpawnHalted = true
	o.Sup.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	time.Sleep(250 * time.Millisecond)
	var spawned int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM agents WHERE role = 'smokealarm'`).Scan(&spawned); err != nil {
		t.Fatal(err)
	}
	if spawned != 0 {
		t.Fatalf("smoke alarms spawned during halt: %d", spawned)
	}
	var events int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_spawned' AND detail LIKE 'role=smokealarm%'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("smoke spawn events during halt: %d", events)
	}
	o.Sup.ResumeSpawning("user")
	waitFor(t, 300*time.Millisecond, "deferred per-agent smoke alarms", func() bool {
		return smokeRows(t, o) == 2
	})
}

func smokeRows(t *testing.T, o *office) int {
	t.Helper()
	var n int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM agents WHERE role = 'smokealarm'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAllModeSmokeRunOnStartAndTicksWaitForResume(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "all", Interval: config.Duration(400 * time.Millisecond), TailLines: 20,
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work"); err != nil {
		t.Fatal(err)
	}
	o.Sup.mu.Lock()
	o.Sup.ceoSpawnHalted = true
	o.Sup.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	time.Sleep(850 * time.Millisecond)
	if n := smokeRows(t, o); n != 0 {
		t.Fatalf("all-mode alarms during halt: %d", n)
	}
	o.Sup.ResumeSpawning("user")
	waitFor(t, 300*time.Millisecond, "due all-mode smoke round", func() bool { return smokeRows(t, o) == 1 })
}

func TestSmokeResumeBeforeIntervalKeepsCadence(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, Mode: "all", Interval: config.Duration(600 * time.Millisecond), TailLines: 20,
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work"); err != nil {
		t.Fatal(err)
	}
	o.Sup.mu.Lock()
	o.Sup.ceoSpawnHalted = true
	o.Sup.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	time.Sleep(100 * time.Millisecond)
	o.Sup.ResumeSpawning("user")
	time.Sleep(200 * time.Millisecond)
	if n := smokeRows(t, o); n != 0 {
		t.Fatalf("smoke round started before interval elapsed: %d", n)
	}
	waitFor(t, time.Second, "normal smoke interval", func() bool { return smokeRows(t, o) == 1 })
}

func TestSmokeTimeoutDoesNotReplaceAlarmDuringHalt(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "all", Interval: config.Duration(time.Hour),
		Timeout: config.Duration(250 * time.Millisecond), TailLines: 20,
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	waitFor(t, 5*time.Second, "initial smoke alarm", func() bool { return smokeRows(t, o) == 1 })
	alarm := onlyAgentOfRole(t, o, "smokealarm")
	o.Sup.mu.Lock()
	o.Sup.ceoSpawnHalted = true
	o.Sup.mu.Unlock()
	time.Sleep(350 * time.Millisecond)
	if n := smokeRows(t, o); n != 1 || agentState(t, o, alarm) == "dead" {
		t.Fatalf("timed-out alarm changed during halt: rows=%d state=%s", n, agentState(t, o, alarm))
	}
	var timeouts int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'smokealarm_timeout'`).Scan(&timeouts); err != nil {
		t.Fatal(err)
	}
	if timeouts != 0 {
		t.Fatalf("timeout events during halt: %d", timeouts)
	}
	o.Sup.ResumeSpawning("user")
	waitFor(t, 5*time.Second, "timed-out round after resume", func() bool { return smokeRows(t, o) >= 2 })
}

func TestActiveSmokeRoundCanFileIncidentDuringHalt(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer":  "ready\nsleep|60s\n",
		"smokealarm":  "ready\nsleep|60s\n",
		"firefighter": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "all", Interval: config.Duration(time.Hour), TailLines: 20,
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	waitFor(t, 5*time.Second, "active smoke round", func() bool {
		return agentState(t, o, onlyAgentOfRole(t, o, "smokealarm")) == "working"
	})
	alarm := onlyAgentOfRole(t, o, "smokealarm")
	o.Sup.mu.Lock()
	o.Sup.ceoSpawnHalted = true
	o.Sup.mu.Unlock()
	if err := sockc.Call(o.Sup.SocketPath, alarm, "incident.create",
		proto.IncidentCreateArgs{Agent: "freelancer-x", Class: "stuck", Detail: "no progress"}, nil); err != nil {
		t.Fatalf("active alarm incident during halt: %v", err)
	}
	waitFor(t, 5*time.Second, "firefighter for active alarm incident", func() bool {
		n, _ := db.CountLivingByRole(o.DB, "firefighter")
		return n == 1
	})
	if state := agentState(t, o, alarm); state == "dead" || state == "" {
		t.Fatalf("active alarm state after halt and incident = %q", state)
	}
}

func TestSmokeRunOnStartWaitsForSafeModeResume(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":        "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "all", Interval: config.Duration(time.Hour), TailLines: 20,
	}
	o.Sup.EnterSafeMode()
	if _, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	time.Sleep(150 * time.Millisecond)
	if n := smokeRows(t, o); n != 0 {
		t.Fatalf("smoke alarms in safe mode: %d", n)
	}
	o.Sup.ResumeSpawning("user")
	waitFor(t, 300*time.Millisecond, "safe-mode deferred smoke alarm", func() bool { return smokeRows(t, o) == 1 })
}

func TestSmokeCapacityWakeStaysPendingDuringHalt(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, Mode: "all", Interval: config.Duration(time.Hour), TailLines: 20,
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work"); err != nil {
		t.Fatal(err)
	}
	o.Sup.deferManagementSpawn(capacitySpawn{role: "smokealarm", profile: "smokealarm", dir: o.Dir, goal: "deferred", configured: true, managementRestart: true})
	o.Sup.mu.Lock()
	o.Sup.ceoSpawnHalted = true
	o.Sup.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	o.Sup.smokeCapacityWake <- struct{}{}
	time.Sleep(150 * time.Millisecond)
	if n := smokeRows(t, o); n != 0 {
		t.Fatalf("capacity wake spawned during halt: %d", n)
	}
	o.Sup.mu.Lock()
	pending := len(o.Sup.pendingSmoke)
	o.Sup.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending smoke requests = %d, want 1", pending)
	}
	o.Sup.ResumeSpawning("user")
	waitFor(t, 300*time.Millisecond, "deferred capacity alarm", func() bool { return smokeRows(t, o) == 1 })
}

func TestOfficePauseDefersSmokeUntilResume(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "all", Interval: config.Duration(400 * time.Millisecond), TailLines: 20,
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work"); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "office.pause", nil, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	time.Sleep(850 * time.Millisecond)
	if n := smokeRows(t, o); n != 0 {
		t.Fatalf("alarms during office pause: %d", n)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "office.resume", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 300*time.Millisecond, "due smoke round after office resume", func() bool { return smokeRows(t, o) == 1 })
}

func TestSafeShutdownPreventsSmokeRunOnStart(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "all", Interval: config.Duration(100 * time.Millisecond), TailLines: 20,
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work"); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.beginSafeShutdown("user", "test"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	time.Sleep(250 * time.Millisecond)
	if n := smokeRows(t, o); n != 0 {
		t.Fatalf("alarms during safe shutdown: %d", n)
	}
}

func TestSmokeLoopRestartsRoundThatExceedsTimeout(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "all", Interval: config.Duration(time.Hour),
		Timeout: config.Duration(250 * time.Millisecond), TailLines: 20,
	}
	victim, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "victim up", func() bool { return agentState(t, o, victim) == "working" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	waitFor(t, 5*time.Second, "timed-out smoke alarm replaced", func() bool {
		var spawned, timedOut int
		o.DB.QueryRow(`SELECT COUNT(*) FROM agents WHERE role = 'smokealarm'`).Scan(&spawned)
		o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'smokealarm_timeout'`).Scan(&timedOut)
		living, _ := db.CountLivingByRole(o.DB, "smokealarm")
		return spawned >= 2 && timedOut >= 1 && living == 1
	})
	var dead int
	o.DB.QueryRow(`SELECT COUNT(*) FROM agents WHERE role = 'smokealarm' AND state = 'dead'`).Scan(&dead)
	if dead < 1 {
		t.Fatal("timed-out smoke alarm was not marked dead")
	}
}

func TestSmokeLoopRestartsRoundWhoseAlarmDiedBeforeTimeout(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "all", Interval: config.Duration(time.Hour),
		Timeout: config.Duration(250 * time.Millisecond), TailLines: 20,
	}
	victim, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "victim up", func() bool { return agentState(t, o, victim) == "working" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	waitFor(t, 5*time.Second, "dead smoke alarm replaced", func() bool {
		var spawned, timedOut int
		o.DB.QueryRow(`SELECT COUNT(*) FROM agents WHERE role = 'smokealarm'`).Scan(&spawned)
		o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'smokealarm_timeout'`).Scan(&timedOut)
		return spawned >= 2 && timedOut >= 1
	})
}
