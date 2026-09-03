package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
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
		_, err := o.Sup.waitVerb(name, 0)
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

func TestWaitTimeoutRestoresWorkingState(t *testing.T) {
	o := newOffice(t, nil)
	const name = "pm-timeout"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "product_manager", Profile: "product_manager"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, name, "working"); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	response, err := o.Sup.waitVerb(name, 25*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if response.Reason != "timeout" {
		t.Fatalf("wait reason = %q", response.Reason)
	}
	if time.Since(started) < 20*time.Millisecond {
		t.Fatal("wait returned before its timeout")
	}
	if got := agentState(t, o, name); got != "working" {
		t.Fatalf("agent state = %s, want working", got)
	}
	o.Sup.mu.Lock()
	_, registered := o.Sup.waiters[name]
	o.Sup.mu.Unlock()
	if registered {
		t.Fatal("timed-out waiter remained registered")
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

func TestDoneCompletesFreelancerJobAndRetainsAgent(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":        "ready\nsleep|30s\n",
		"freelancer": "ready\ndone|research delivered\nwait\nsend|ceo|follow-up answer|context retained\nwait\n",
	})
	ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "ceo ready", func() bool { return agentState(t, o, ceo) == "working" })
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
	waitFor(t, 5*time.Second, "freelancer retained for follow-up", func() bool { return agentState(t, o, name) == "waiting" })
	if _, err := o.Sup.Mail.Send(ceo, name, "follow-up question", "what supports that?", bus.PrioNormal); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "freelancer answers with retained context", func() bool {
		inbox, _ := o.Sup.Mail.Inbox(ceo)
		for _, msg := range inbox {
			if msg.Subject == "follow-up answer" && msg.Body == "context retained" {
				return true
			}
		}
		return false
	})
}

func TestMailNotificationTypedOncePerUnreadInbox(t *testing.T) {
	// The notification lands on the PTY of the (still running) fake agent; we
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
	waitFor(t, 10*time.Second, "mail notification on ceo screen", func() bool {
		return strings.Contains(sess.Screen(), o.Sup.Msgs.MailNudge())
	})
	// And the message is durably in the inbox.
	inbox, _ := o.Sup.Mail.Inbox(ceo)
	if len(inbox) != 1 || inbox[0].Subject != "hi" {
		t.Fatalf("inbox = %+v", inbox)
	}
	notifications := strings.Count(sess.Screen(), o.Sup.Msgs.MailNudge())
	second, err := o.Sup.Mail.Send(bus.SystemSender, ceo, "reminder", "check the first message", bus.PrioNormal)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if got := strings.Count(sess.Screen(), o.Sup.Msgs.MailNudge()); got != notifications {
		t.Fatalf("notification count with unread mail = %d, want %d", got, notifications)
	}

	if _, err := o.Sup.Mail.Read(ceo, inbox[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.Mail.Read(ceo, second[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.Mail.Send(bus.SystemSender, ceo, "new mail", "after clearing inbox", bus.PrioNormal); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "new notification after clearing inbox", func() bool {
		return strings.Count(sess.Screen(), o.Sup.Msgs.MailNudge()) == notifications+1
	})
}

func TestMailNotificationFlushesAfterTypingDebounceWithoutScheduler(t *testing.T) {
	o := newOffice(t, map[string]string{"ceo": "ready\nsleep|10s\n"})
	o.Sup.Cfg.Notifications.InputDebounce = config.Duration(200 * time.Millisecond)
	ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "ceo working", func() bool { return agentState(t, o, ceo) == "working" })
	sess, _ := o.Sup.Session(ceo)
	o.Sup.SetInteraction(ceo, true)
	o.Sup.RecordUserInput(ceo)
	if _, err := o.Sup.Mail.Send(bus.SystemSender, ceo, "mail", "body", bus.PrioNormal); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "mail notification pending", func() bool {
		return o.Sup.MailNotificationPending(ceo)
	})
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(sess.Screen(), o.Sup.Msgs.MailNudge()) {
		t.Fatal("mail notification bypassed the typing debounce")
	}
	waitFor(t, 2*time.Second, "mail notification after debounce", func() bool {
		return strings.Contains(sess.Screen(), o.Sup.Msgs.MailNudge())
	})
}

func TestPendingMailNotificationClearsWhenMailWasReadDuringDebounce(t *testing.T) {
	o := newOffice(t, map[string]string{"ceo": "ready\nsleep|10s\n"})
	o.Sup.Cfg.Notifications.InputDebounce = config.Duration(150 * time.Millisecond)
	ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "ceo working", func() bool { return agentState(t, o, ceo) == "working" })
	sess, _ := o.Sup.Session(ceo)
	o.Sup.SetInteraction(ceo, true)
	o.Sup.RecordUserInput(ceo)
	ids, err := o.Sup.Mail.Send(bus.SystemSender, ceo, "mail", "body", bus.PrioNormal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.Mail.Read(ceo, ids[0]); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "stale pending notification cleared", func() bool {
		return !o.Sup.MailNotificationPending(ceo)
	})
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(sess.Screen(), o.Sup.Msgs.MailNudge()) {
		t.Fatal("read mail produced a stale delayed notification")
	}
}

func TestKillAllWaitsForSessionExitCleanup(t *testing.T) {
	o := newOffice(t, nil)
	cleanupFinished := make(chan struct{})
	o.Sup.sessionWatchers.Add(1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		o.Sup.sessionWatchers.Done()
		close(cleanupFinished)
	}()

	o.Sup.KillAll()
	select {
	case <-cleanupFinished:
	default:
		t.Fatal("KillAll returned while session exit cleanup was still running")
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
