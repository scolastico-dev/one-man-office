package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestContextSaveVerbPersistsAuthenticatedRoleAndJob(t *testing.T) {
	o := newOffice(t, nil)
	const name = "developer-context"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "developer", Profile: "developer", JobID: 9}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, name, "working"); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, name, "context.save", proto.ContextSaveArgs{Summary: "tests pass; commit remains"}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetShutdownContext(o.DB, "developer", 9)
	if err != nil || got == nil || got.Agent != name || got.Context != "tests pass; commit remains" {
		t.Fatalf("saved context = %+v, err %v", got, err)
	}
}

func TestSafeShutdownTreatsRetainedCompletedFreelancerAsComplete(t *testing.T) {
	o := newOffice(t, nil)
	j := &queue.Job{Title: "research", Goal: "g", Role: "freelancer"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking, queue.StateMerging, queue.StateDone} {
		if err := o.Sup.Jobs.Transition(j.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	if !o.Sup.safeShutdownRoleComplete(db.Agent{Role: "freelancer", JobID: j.ID}) {
		t.Fatal("completed retained freelancer would block safe shutdown or save an unrestorable handoff")
	}
}

func TestReadyRestoresThenDeletesShutdownContext(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.SaveShutdownContext(o.DB, db.ShutdownContext{
		Role: "freelancer", JobID: 0, Agent: "freelancer-old", Context: "research gathered; send the report",
	}); err != nil {
		t.Fatal(err)
	}
	const name = "freelancer-new"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "freelancer", Profile: "freelancer", Goal: "continue"}); err != nil {
		t.Fatal(err)
	}
	response, err := o.Sup.ready(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SAFE-SHUTDOWN HANDOFF", "research gathered; send the report"} {
		if !strings.Contains(response.Prompt, want) {
			t.Errorf("restored prompt missing %q:\n%s", want, response.Prompt)
		}
	}
	if got, err := db.GetShutdownContext(o.DB, "freelancer", 0); err != nil || got != nil {
		t.Fatalf("restored context was not deleted: %+v, err %v", got, err)
	}
}

func TestSafeShutdownBroadcastsAndStopsAfterAgentExits(t *testing.T) {
	oldTimeout := SafeShutdownTimeout
	SafeShutdownTimeout = 2 * time.Second
	t.Cleanup(func() { SafeShutdownTimeout = oldTimeout })
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "wait for mail")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "agent waiting", func() bool { return agentState(t, o, name) == "waiting" })
	if err := o.Sup.BeginSafeShutdown("user"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-o.Sup.EmergencyStop():
	case <-time.After(5 * time.Second):
		t.Fatal("safe shutdown did not stop after target agent exited")
	}
	history, err := o.Sup.Mail.History()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range history {
		if message.To == name && message.Subject == "safe shutdown requested" && strings.Contains(message.Body, "omo context save") {
			found = true
		}
	}
	if !found {
		t.Fatalf("safe-shutdown broadcast missing: %+v", history)
	}
}
