package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestSpawnHandshakeAndWait(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nwait\n",
	})
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "sit tight")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "freelancer-") {
		t.Fatalf("bad name %q", name)
	}
	waitFor(t, 5*time.Second, "agent waiting", func() bool { return agentState(t, o, name) == "waiting" })
	// Wake it: session should finish the wait verb and the scenario ends.
	o.Sup.WakeAgent(name)
	sess, _ := o.Sup.Session(name)
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not exit after wake")
	}
}

func TestWaitReturnsWhenMailArrivedBeforeWaiterRegistration(t *testing.T) {
	o := newOffice(t, nil)
	const name = "pm-ada"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "product_manager", Profile: "product_manager"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, name, "working"); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.Mail.Send("user", name, "job merged", "job #2 is done", bus.PrioHigh); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := o.Sup.waitVerb(name)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		o.Sup.WakeAgent(name)
		<-done
		t.Fatal("wait blocked even though the agent already had unread mail")
	}
	if got := agentState(t, o, name); got != "working" {
		t.Fatalf("agent state = %s, want working", got)
	}
}

func TestHandshakeTimeoutRetriesThenFails(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "hang\n",
	})
	j := &queue.Job{Title: "t", Goal: "g", Role: "freelancer"}
	o.Sup.Jobs.Create(j)
	o.Sup.Jobs.Transition(j.ID, queue.StateAssigned)
	if _, err := o.Sup.Spawn("freelancer", "freelancer", j.ID, o.Dir, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "job failed and failure announced after spawn retries", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		if got.State != queue.StateFailed {
			return false
		}
		n, _ := o.Sup.Mail.UnreadCount("user")
		return n > 0
	})
}

func TestDoneMarksAgentAndJob(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\ndone|research delivered\n",
	})
	j := &queue.Job{Title: "r", Goal: "research X", Role: "freelancer"}
	o.Sup.Jobs.Create(j)
	o.Sup.Jobs.Transition(j.ID, queue.StateAssigned)
	name, err := o.Sup.Spawn("freelancer", "freelancer", j.ID, o.Dir, "")
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Jobs.SetAssignee(j.ID, name)
	waitFor(t, 10*time.Second, "job done", func() bool {
		got, _ := o.Sup.Jobs.Get(j.ID)
		return got.State == queue.StateDone && got.Result == "research delivered"
	})
	waitFor(t, 5*time.Second, "agent done", func() bool { return agentState(t, o, name) == "done" })
}

func TestMailNudgeTypedIntoRunningSession(t *testing.T) {
	// The nudge lands on the PTY of the (still running) fake agent; we
	// assert via the session screen.
	o := newOffice(t, map[string]string{
		"ceo":        "ready\nsleep|10s\n",
		"freelancer": "ready\nsend|ceo|hi|hello ceo\ndone|sent\n",
	})
	ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "ceo working", func() bool { return agentState(t, o, ceo) == "working" })
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "send mail"); err != nil {
		t.Fatal(err)
	}
	sess, _ := o.Sup.Session(ceo)
	// Match the full nudge line: the CEO's own role prompt (echoed by
	// `omo ready`) quotes "You have new mail." on its own, so a shorter
	// substring would match before any mail has been sent.
	waitFor(t, 10*time.Second, "nudge on ceo screen", func() bool {
		return strings.Contains(sess.Screen(), o.Sup.Msgs.MailNudge())
	})
	// And the message is durably in the inbox.
	inbox, _ := o.Sup.Mail.Inbox(ceo)
	if len(inbox) != 1 || inbox[0].Subject != "hi" {
		t.Fatalf("inbox = %+v", inbox)
	}
}

func TestAuthRejectsUnknownAgent(t *testing.T) {
	o := newOffice(t, map[string]string{})
	if err := o.Sup.Auth("ghost", "inbox"); err == nil {
		t.Fatal("unknown agent must be rejected")
	}
}

func TestStepAndAgentList(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "ready\nsleep|10s\n"})
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "research")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "agent working", func() bool { return agentState(t, o, name) == "working" })
	if err := sockc.Call(o.Sup.SocketPath, name, "step", proto.StepArgs{Description: "checking upstream documentation"}, nil); err != nil {
		t.Fatal(err)
	}
	var agents []proto.AgentStatus
	if err := sockc.Call(o.Sup.SocketPath, name, "agent.list", nil, &agents); err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Name != name || agents[0].Step != "checking upstream documentation" || agents[0].StepUpdatedAt == "" {
		t.Fatalf("agent list = %+v", agents)
	}
	if err := sockc.Call(o.Sup.SocketPath, name, "step", proto.StepArgs{Description: "   "}, nil); err == nil {
		t.Fatal("blank step must be rejected")
	}
}
