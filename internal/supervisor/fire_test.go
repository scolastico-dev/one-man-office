package supervisor

import (
	"testing"
	"time"

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

func TestAgentKillByRoleRequeuesJob(t *testing.T) {
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
	waitFor(t, 15*time.Second, "job requeued with restart note", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return (got.State == queue.StateQueued || got.State == queue.StateWorking) && got.Note == queue.RestartNote
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
