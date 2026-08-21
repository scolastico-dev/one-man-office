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
	for _, want := range []string{name, "freelancer", "long research goal", "PM lateral messages since last round"} {
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
	report3 := o.Sup.smokeReport()
	if strings.Contains(report3, "round-two-marker") {
		t.Error("third report repeats old events — delta tracking broken")
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

func TestPerAgentSmokeModeStartsOneAlarmPerAgentDespiteSpawnHalt(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{
		Enabled: true, RunOnStart: true, Mode: "per_agent", Interval: config.Duration(time.Hour), TailLines: 20,
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
	waitFor(t, 5*time.Second, "per-agent smoke alarms", func() bool {
		n, _ := db.CountLivingByRole(o.DB, "smokealarm")
		return n == 2
	})
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
