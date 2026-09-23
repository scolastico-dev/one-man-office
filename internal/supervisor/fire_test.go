package supervisor

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

// spawnFF puts a firefighter up manually and returns its name.
func spawnFF(t *testing.T, o *office) string {
	t.Helper()
	name, err := o.Sup.Spawn("firefighter", "firefighter", 0, o.Dir, "INCIDENT_ID: 1\nmanual")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "firefighter up", func() bool { return agentState(t, o, name) == "working" })
	return name
}

func TestPauseBlocksDispatchResumeUnblocks(t *testing.T) {
	o := newOffice(t, map[string]string{
		"firefighter": "ready\nsleep|60s\n",
		"freelancer":  "ready\ndone|ok\n",
	})
	startDispatch(t, o)
	ff := spawnFF(t, o)
	if err := sockc.Call(o.Sup.SocketPath, ff, "office.pause", nil, nil); err != nil {
		t.Fatal(err)
	}
	j := &queue.Job{Title: "t", Goal: "g", Role: "freelancer"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	time.Sleep(2 * time.Second)
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.State != queue.StateQueued {
		t.Fatalf("job dispatched while paused: %s", got.State)
	}
	if err := sockc.Call(o.Sup.SocketPath, ff, "office.resume", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "job runs after resume", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateDone
	})
}

func TestAgentKillByRoleCancelsJobWithoutReplacement(t *testing.T) {
	o := newOffice(t, map[string]string{
		"firefighter": "ready\nsleep|60s\n",
		"freelancer":  "ready\nsleep|60s\n",
	})
	startDispatch(t, o)
	ff := spawnFF(t, o)
	j := &queue.Job{Title: "victim", Goal: "g", Role: "freelancer"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	waitFor(t, 15*time.Second, "victim working", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateWorking
	})
	if err := sockc.Call(o.Sup.SocketPath, ff, "agent.kill", proto.AgentNameArgs{Name: "freelancer"}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "job cancelled", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateCancelled
	})
	time.Sleep(200 * time.Millisecond)
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.Retries != 0 || got.Note != "" {
		t.Fatalf("kill retried job: retries=%d note=%q", got.Retries, got.Note)
	}
	if living, _ := db.LivingByRole(o.DB, "freelancer"); len(living) != 0 {
		t.Fatalf("kill spawned replacement agents: %+v", living)
	}
}

func TestAgentRestartReplacesAgentWithoutRequeue(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":        "ready\nsleep|60s\n",
		"freelancer": "ready\nsleep|60s\n",
	})
	startDispatch(t, o)
	ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "ceo ready", func() bool { return agentState(t, o, ceo) == "working" })
	j := &queue.Job{Title: "victim", Goal: "g", Role: "freelancer"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	waitFor(t, 15*time.Second, "victim working", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateWorking
	})
	before, _ := o.Sup.Jobs.Get(j.ID)
	if err := sockc.Call(o.Sup.SocketPath, ceo, "agent.restart", proto.AgentNameArgs{Name: "freelancer"}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "replacement agent working", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateWorking && got.Assignee != "" && got.Assignee != before.Assignee
	})
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.Retries != 0 || got.Note != "" {
		t.Fatalf("restart requeued job: state=%s retries=%d note=%q", got.State, got.Retries, got.Note)
	}
	events, err := db.EventsSince(o.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	requested, replaced := false, false
	for _, event := range events {
		if event.JobID != j.ID {
			continue
		}
		if event.Kind == "agent_restart_requested" {
			requested = true
		}
		if event.Kind == "agent_restarted" {
			replaced = true
		}
		if event.Kind == "job_state" && event.Detail == "working→queued" {
			t.Fatalf("restart requeued job: %+v", event)
		}
	}
	if !requested || !replaced {
		t.Fatalf("restart events missing: requested=%v replaced=%v", requested, replaced)
	}
	waitFor(t, 5*time.Second, "old agent dead", func() bool {
		return agentState(t, o, before.Assignee) == "dead"
	})
}

func TestGatingNonFirefighterDenied(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|60s\n",
	})
	name, _ := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "idle")
	waitFor(t, 5*time.Second, "up", func() bool { return agentState(t, o, name) == "working" })
	if err := sockc.Call(o.Sup.SocketPath, name, "office.pause", nil, nil); err == nil {
		t.Fatal("freelancer must not pause the office")
	}
	if err := sockc.Call(o.Sup.SocketPath, name, "agent.kill", proto.AgentNameArgs{Name: "x"}, nil); err == nil {
		t.Fatal("freelancer must not kill agents")
	}
}

func TestEmergencyStopRoleGate(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":         "ready\nsleep|60s\n",
		"firefighter": "ready\nsleep|60s\n",
		"freelancer":  "ready\nsleep|60s\n",
	})
	ceo, _ := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run")
	ff, _ := o.Sup.Spawn("firefighter", "firefighter", 0, o.Dir, "INCIDENT_ID: 1\nstop")
	free, _ := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	waitFor(t, 5*time.Second, "agents ready", func() bool {
		return agentState(t, o, ceo) == "working" && agentState(t, o, ff) == "working" && agentState(t, o, free) == "working"
	})

	if err := sockc.Call(o.Sup.SocketPath, free, "office.estop", nil, nil); err == nil {
		t.Fatal("freelancer must not emergency-stop omo")
	}
	if err := sockc.Call(o.Sup.SocketPath, ff, "office.estop", nil, nil); err != nil {
		t.Fatalf("firefighter emergency stop: %v", err)
	}
	if err := sockc.Call(o.Sup.SocketPath, ceo, "office.estop", nil, nil); err != nil {
		t.Fatalf("CEO emergency stop: %v", err)
	}
	select {
	case <-o.Sup.EmergencyStop():
	case <-time.After(time.Second):
		t.Fatal("CEO emergency stop did not signal the office")
	}
}

func TestUserCanEmergencyStop(t *testing.T) {
	o := newOffice(t, nil)
	if err := sockc.Call(o.Sup.SocketPath, "user", "office.estop", nil, nil); err != nil {
		t.Fatalf("user emergency stop: %v", err)
	}
	select {
	case <-o.Sup.EmergencyStop():
	case <-time.After(time.Second):
		t.Fatal("user emergency stop did not signal the office")
	}
}

func TestCEOCanHaltWorkSpawnsButNotSmokeAlarm(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":        "ready\nsleep|60s\n",
		"freelancer": "ready\nsleep|60s\n",
		"smokealarm": "ready\nsleep|60s\n",
	})
	ceo, _ := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	waitFor(t, 5*time.Second, "ceo up", func() bool { return agentState(t, o, ceo) == "working" })
	if err := sockc.Call(o.Sup.SocketPath, ceo, "office.halt-spawns", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "blocked"); !errors.Is(err, ErrSpawningHalted) {
		t.Fatalf("work spawn error = %v", err)
	}
	smoke, err := o.Sup.Spawn("smokealarm", "smokealarm", 0, o.Dir, "inspect")
	if err != nil {
		t.Fatalf("smoke alarm blocked by CEO halt: %v", err)
	}
	waitFor(t, 5*time.Second, "smoke alarm up", func() bool { return agentState(t, o, smoke) == "working" })
	if err := sockc.Call(o.Sup.SocketPath, ceo, "office.resume-spawns", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "allowed"); err != nil {
		t.Fatalf("work spawn after resume: %v", err)
	}
}

func TestIncidentResolveReportsToUser(t *testing.T) {
	o := newOffice(t, map[string]string{
		"firefighter": "ready\nsleep|60s\n",
	})
	o.DB.Exec(`INSERT INTO incidents (agent, class, detail) VALUES ('x', 'stuck', 'd')`)
	ff := spawnFF(t, o)
	if err := sockc.Call(o.Sup.SocketPath, ff, "incident.resolve",
		proto.IncidentResolveArgs{ID: 1, Report: "restarted x, all good"}, nil); err != nil {
		t.Fatal(err)
	}
	var state string
	o.DB.QueryRow(`SELECT state FROM incidents WHERE id = 1`).Scan(&state)
	if state != "resolved" {
		t.Fatalf("incident state = %q", state)
	}
	if n, _ := o.Sup.Mail.UnreadCount("user"); n == 0 {
		t.Fatal("report to user missing")
	}
}

func TestFirefighterOwnsIncidentAndCannotWaitAfterResolution(t *testing.T) {
	o := newOffice(t, map[string]string{
		"smokealarm":  "ready\nsleep|60s\n",
		"firefighter": "ready\nsleep|60s\n",
	})
	smoke, err := o.Sup.Spawn("smokealarm", "smokealarm", 0, o.Dir, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "smoke alarm ready", func() bool { return agentState(t, o, smoke) == "working" })
	if err := sockc.Call(o.Sup.SocketPath, smoke, "incident.create", proto.IncidentCreateArgs{
		Agent: "developer-x", Class: "stuck", Detail: "no progress",
	}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "open incident and firefighter", func() bool {
		var open int
		if err := o.DB.QueryRow(`SELECT COUNT(*) FROM incidents WHERE state = 'open'`).Scan(&open); err != nil || open != 1 {
			return false
		}
		n, _ := db.CountLivingByRole(o.DB, "firefighter")
		return n == 1
	})
	ff := onlyAgentOfRole(t, o, "firefighter")
	firefighter, err := db.GetAgent(o.DB, ff)
	if err != nil {
		t.Fatal(err)
	}
	if firefighter.IncidentID == 0 {
		t.Fatal("firefighter incident association was not persisted")
	}
	incidentID := firefighter.IncidentID
	if _, err := o.Sup.waitVerb(ff, 10*time.Millisecond); err != nil {
		t.Fatalf("open-incident firefighter wait: %v", err)
	}
	if err := sockc.Call(o.Sup.SocketPath, ff, "incident.resolve", proto.IncidentResolveArgs{
		ID: incidentID, Report: "fixed the unhealthy agent",
	}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "incident_resolved event", func() bool {
		var n int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'incident_resolved' AND detail = ?`, fmt.Sprintf("#%d", incidentID)).Scan(&n) == nil && n == 1
	})
	_, err = o.Sup.waitVerb(ff, 10*time.Millisecond)
	want := fmt.Sprintf("incident %d is resolved; a firefighter never parks — finish now with `omo done \"incident %d resolved\"`. Smoke alarms stay suspended while you are alive.", incidentID, incidentID)
	if err == nil || err.Error() != want {
		t.Fatalf("wait error = %q, want %q", err, want)
	}
	var state string
	if err := o.DB.QueryRow(`SELECT state FROM agents WHERE name = ?`, ff).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "working" {
		t.Fatalf("resolved firefighter state = %q, want working after rejected wait", state)
	}

	missing := db.Agent{Name: "firefighter-missing", Role: "firefighter", Profile: "firefighter", IncidentID: incidentID + 1000}
	if err := db.InsertAgent(o.DB, missing); err != nil {
		t.Fatal(err)
	}
	_, err = o.Sup.waitVerb(missing.Name, 10*time.Millisecond)
	missingWant := fmt.Sprintf("incident %d is resolved; a firefighter never parks — finish now with `omo done \"incident %d resolved\"`. Smoke alarms stay suspended while you are alive.", missing.IncidentID, missing.IncidentID)
	if err == nil || err.Error() != missingWant {
		t.Fatalf("missing incident wait error = %q, want %q", err, missingWant)
	}
	unassociated := db.Agent{Name: "firefighter-unassociated", Role: "firefighter", Profile: "firefighter"}
	if err := db.InsertAgent(o.DB, unassociated); err != nil {
		t.Fatal(err)
	}
	_, err = o.Sup.waitVerb(unassociated.Name, 10*time.Millisecond)
	unassociatedWant := fmt.Sprintf("incident %d is resolved; a firefighter never parks — finish now with `omo done \"incident %d resolved\"`. Smoke alarms stay suspended while you are alive.", unassociated.IncidentID, unassociated.IncidentID)
	if err == nil || err.Error() != unassociatedWant {
		t.Fatalf("unassociated wait error = %q, want %q", err, unassociatedWant)
	}
}

func TestFirefighterRestartRetainsIncidentOwnership(t *testing.T) {
	o := newOffice(t, map[string]string{
		"firefighter": "ready\nsleep|60s\n",
	})
	result, err := o.DB.Exec(`INSERT INTO incidents (agent, class, detail) VALUES ('developer-x', 'stuck', 'no progress')`)
	if err != nil {
		t.Fatal(err)
	}
	incidentID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	ff, err := o.Sup.spawnRoleForIncident("firefighter", incidentID, 0, o.Dir, "INCIDENT_ID: 1\nmanual", 0)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "firefighter ready", func() bool { return agentState(t, o, ff) == "working" })
	if _, err := o.DB.Exec(`UPDATE incidents SET state = 'resolved' WHERE id = ?`, incidentID); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.StopAgent(ff, "user", "restart"); err != nil {
		t.Fatalf("restart firefighter: %v", err)
	}
	waitFor(t, 5*time.Second, "firefighter replacement", func() bool {
		n, _ := db.CountLivingByRole(o.DB, "firefighter")
		return n == 1
	})
	replacement := onlyAgentOfRole(t, o, "firefighter")
	a, err := db.GetAgent(o.DB, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if a.IncidentID != incidentID {
		t.Fatalf("replacement incident id = %d, want %d", a.IncidentID, incidentID)
	}
}

func TestJobCancelAndRequeue(t *testing.T) {
	o := newOffice(t, map[string]string{
		"firefighter": "ready\nsleep|60s\n",
	})
	ff := spawnFF(t, o)
	j := &queue.Job{Title: "t", Goal: "g", Role: "freelancer"}
	o.Sup.Jobs.Create(j)
	if err := sockc.Call(o.Sup.SocketPath, ff, "job.cancel", proto.JobIDArgs{ID: j.ID}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := o.Sup.Jobs.Get(j.ID)
	if got.State != queue.StateCancelled {
		t.Fatalf("state = %s", got.State)
	}
	if err := sockc.Call(o.Sup.SocketPath, ff, "job.requeue", proto.JobIDArgs{ID: j.ID}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = o.Sup.Jobs.Get(j.ID)
	if got.State != queue.StateQueued {
		t.Fatalf("state = %s", got.State)
	}
}

func TestUserAndCEOCanRunManagementVerbs(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":        "ready\nsleep|60s\n",
		"freelancer": "ready\nsleep|60s\n",
	})
	ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "ceo ready", func() bool { return agentState(t, o, ceo) == "working" })

	for _, caller := range []string{"user", ceo} {
		j := &queue.Job{Title: caller + " job", Goal: "g", Role: "freelancer"}
		if err := o.Sup.Jobs.Create(j); err != nil {
			t.Fatal(err)
		}
		if err := sockc.Call(o.Sup.SocketPath, caller, "job.cancel", proto.JobIDArgs{ID: j.ID}, nil); err != nil {
			t.Fatalf("%s cancel: %v", caller, err)
		}
		if err := sockc.Call(o.Sup.SocketPath, caller, "job.requeue", proto.JobIDArgs{ID: j.ID}, nil); err != nil {
			t.Fatalf("%s requeue: %v", caller, err)
		}
		if err := sockc.Call(o.Sup.SocketPath, caller, "office.pause", nil, nil); err != nil {
			t.Fatalf("%s pause: %v", caller, err)
		}
		if err := sockc.Call(o.Sup.SocketPath, caller, "office.resume", nil, nil); err != nil {
			t.Fatalf("%s resume: %v", caller, err)
		}
	}
}
