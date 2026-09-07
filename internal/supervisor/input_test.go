package supervisor

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/session"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestAgentInputBytesCombinesTextAndNamedKeys(t *testing.T) {
	got, err := agentInputBytes("1", []string{"enter", "up", "ctrl+c", "page-down"})
	if err != nil {
		t.Fatal(err)
	}
	want := "1\r\x1b[A\x03\x1b[6~"
	if got != want {
		t.Fatalf("encoded input = %q, want %q", got, want)
	}
	if _, err := agentInputBytes("", nil); err == nil {
		t.Fatal("empty input accepted")
	}
	if _, err := agentInputBytes("", []string{"f13"}); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestAgentKeyBytesEncodesKeysWithoutText(t *testing.T) {
	got, err := agentKeyBytes([]string{"enter", "up", "ctrl+c"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "\r\x1b[A\x03" {
		t.Fatalf("encoded keys = %q", got)
	}
}

func TestAgentInputIsAvailableToUserCEOFirefighterAndSystem(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":         "ready\nsleep|60s\n",
		"developer":   "ready\nsleep|60s\n",
		"firefighter": "ready\nsleep|60s\n",
		"freelancer":  "ready\nsleep|60s\n",
	})
	ceo, _ := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run")
	developer, _ := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
	ff, _ := o.Sup.Spawn("firefighter", "firefighter", 0, o.Dir, "INCIDENT_ID: 1\ninspect")
	freelancer, _ := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	waitFor(t, 5*time.Second, "agents ready", func() bool {
		return agentState(t, o, ceo) == "working" && agentState(t, o, developer) == "working" &&
			agentState(t, o, ff) == "working" && agentState(t, o, freelancer) == "working"
	})

	for _, caller := range []string{"user", ceo, ff, bus.SystemSender} {
		args := proto.AgentInputArgs{Name: developer, Text: caller, Keys: []string{"enter"}}
		if err := sockc.Call(o.Sup.SocketPath, caller, "agent.input", args, nil); err != nil {
			t.Errorf("%s input: %v", caller, err)
		}
	}
	var events int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_input_sent'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 4 {
		t.Fatalf("input events = %d, want 4", events)
	}
	if err := sockc.Call(o.Sup.SocketPath, freelancer, "agent.input",
		proto.AgentInputArgs{Name: developer, Keys: []string{"enter"}}, nil); err == nil {
		t.Fatal("freelancer sent terminal input")
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "agent.input",
		proto.AgentInputArgs{Name: "missing-agent", Keys: []string{"enter"}}, nil); err == nil {
		t.Fatal("input to missing agent succeeded")
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "agent.input",
		proto.AgentInputArgs{Name: developer, Keys: []string{"f13"}}, nil); err == nil {
		t.Fatal("unknown special key succeeded")
	}
}

func TestAgentInputDoesNotReachSessionWhenAuditPersistenceFails(t *testing.T) {
	o := newOffice(t, map[string]string{"developer": "ready\nsleep|60s\n"})
	developer, err := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "developer ready", func() bool {
		return agentState(t, o, developer) == "working"
	})
	if _, err := o.DB.Exec(`CREATE TRIGGER reject_input_audit
		BEFORE INSERT ON events WHEN NEW.kind = 'agent_input_requested'
		BEGIN SELECT RAISE(FAIL, 'input audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}

	const marker = "INPUT-WITHOUT-AUDIT"
	err = sockc.Call(o.Sup.SocketPath, "user", "agent.input",
		proto.AgentInputArgs{Name: developer, Text: marker}, nil)
	if err == nil || !strings.Contains(err.Error(), "input audit unavailable") {
		t.Fatalf("input with failed audit = %v, want audit error", err)
	}
	sess, _ := o.Sup.Session(developer)
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(sess.Screen(), marker) {
		t.Fatal("input reached the agent after audit persistence failed")
	}
}

func TestAgentInputWaitsWhileHumanIsTypingInWritablePeek(t *testing.T) {
	o := newOffice(t, map[string]string{"developer": "ready\nsleep|60s\n"})
	o.Sup.Cfg.Notifications.InputDebounce = config.Duration(25 * time.Millisecond)
	developer, err := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "developer ready", func() bool {
		return agentState(t, o, developer) == "working"
	})
	o.Sup.SetInteraction(developer, true)
	o.Sup.RecordUserInput(developer)

	const marker = "QUEUED-PROGRAMMATIC-INPUT"
	done := make(chan error, 1)
	go func() {
		done <- sockc.Call(o.Sup.SocketPath, "user", "agent.input",
			proto.AgentInputArgs{Name: developer, Text: marker, Keys: []string{"enter"}}, nil)
	}()
	waitFor(t, time.Second, "durable input request", func() bool {
		var count int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_input_requested'`).Scan(&count) == nil && count == 1
	})
	if !o.Sup.InputPending(developer) {
		t.Fatal("queued programmatic input was not exposed as pending")
	}
	time.Sleep(100 * time.Millisecond)

	select {
	case err := <-done:
		t.Fatalf("input completed while writable human interaction was active: %v", err)
	default:
	}
	var sent int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_input_sent'`).Scan(&sent); err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatalf("delivery audit count = %d before release, want 0", sent)
	}
	sess, _ := o.Sup.Session(developer)
	if strings.Contains(sess.Screen(), marker) {
		t.Fatal("programmatic input reached the PTY while human input was still protected")
	}

	o.Sup.SetInteraction("", false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued input was not released after leaving writable peek")
	}
	waitFor(t, time.Second, "input visible after release", func() bool {
		return strings.Contains(sess.Screen(), marker)
	})
	if o.Sup.InputPending(developer) {
		t.Fatal("programmatic input remained pending after delivery")
	}
}

func TestQueuedAgentInputPreservesRequestOrder(t *testing.T) {
	o := newOffice(t, map[string]string{"developer": "ready\nsleep|60s\n"})
	o.Sup.Cfg.Notifications.InputDebounce = config.Duration(time.Hour)
	developer, err := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "developer ready", func() bool {
		return agentState(t, o, developer) == "working"
	})
	o.Sup.SetInteraction(developer, true)
	o.Sup.RecordUserInput(developer)

	type result struct {
		caller string
		err    error
	}
	done := make(chan result, 2)
	send := func(caller, text string) {
		go func() {
			err := sockc.Call(o.Sup.SocketPath, caller, "agent.input",
				proto.AgentInputArgs{Name: developer, Text: text, Keys: []string{"enter"}}, nil)
			done <- result{caller: caller, err: err}
		}()
	}
	send("user", "FIRST-QUEUED-INPUT")
	waitFor(t, time.Second, "first durable input request", func() bool {
		var count int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_input_requested'`).Scan(&count) == nil && count == 1
	})
	send(bus.SystemSender, "SECOND-QUEUED-INPUT")
	waitFor(t, time.Second, "second durable input request", func() bool {
		var count int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_input_requested'`).Scan(&count) == nil && count == 2
	})

	o.Sup.SetInteraction("", false)
	for range 2 {
		select {
		case got := <-done:
			if got.err != nil {
				t.Fatalf("%s queued input: %v", got.caller, got.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queued input did not finish")
		}
	}
	sess, _ := o.Sup.Session(developer)
	waitFor(t, time.Second, "both queued inputs visible", func() bool {
		screen := sess.Screen()
		return strings.Contains(screen, "FIRST-QUEUED-INPUT") && strings.Contains(screen, "SECOND-QUEUED-INPUT")
	})
	screen := sess.Screen()
	if strings.Index(screen, "FIRST-QUEUED-INPUT") > strings.Index(screen, "SECOND-QUEUED-INPUT") {
		t.Fatalf("queued input arrived out of order:\n%s", screen)
	}
	events, err := db.EventsSince(o.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	var delivered []string
	for _, event := range events {
		if event.Kind == "agent_input_sent" {
			delivered = append(delivered, event.Agent)
		}
	}
	if len(delivered) != 2 || delivered[0] != "user" || delivered[1] != bus.SystemSender {
		t.Fatalf("delivery audit order = %v, want [user %s]", delivered, bus.SystemSender)
	}
}

func TestAgentInputIsImmediateOutsideWritableTargetCollision(t *testing.T) {
	tests := []struct {
		name       string
		writable   bool
		peekTarget bool
	}{
		{name: "other agent is writable", writable: true},
		{name: "target is read only", peekTarget: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newOffice(t, map[string]string{
				"ceo":       "ready\nsleep|60s\n",
				"developer": "ready\nsleep|60s\n",
			})
			o.Sup.Cfg.Notifications.InputDebounce = config.Duration(time.Hour)
			ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run")
			if err != nil {
				t.Fatal(err)
			}
			developer, err := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, 5*time.Second, "agents ready", func() bool {
				return agentState(t, o, ceo) == "working" && agentState(t, o, developer) == "working"
			})
			peek := ceo
			if tt.peekTarget {
				peek = developer
			}
			o.Sup.SetInteraction(peek, tt.writable)
			o.Sup.RecordUserInput(developer)

			if err := sockc.Call(o.Sup.SocketPath, "user", "agent.input",
				proto.AgentInputArgs{Name: developer, Text: "IMMEDIATE-INPUT"}, nil); err != nil {
				t.Fatal(err)
			}
			var sent int
			if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_input_sent'`).Scan(&sent); err != nil {
				t.Fatal(err)
			}
			if sent != 1 {
				t.Fatalf("delivery audit count = %d, want 1", sent)
			}
		})
	}
}

func TestQueuedAgentInputFailsSafelyWhenSessionEnds(t *testing.T) {
	o := newOffice(t, map[string]string{"developer": "ready\nsleep|60s\n"})
	o.Sup.Cfg.Notifications.InputDebounce = config.Duration(time.Hour)
	developer, err := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "developer ready", func() bool {
		return agentState(t, o, developer) == "working"
	})
	o.Sup.SetInteraction(developer, true)
	o.Sup.RecordUserInput(developer)

	done := make(chan error, 1)
	go func() {
		done <- sockc.Call(o.Sup.SocketPath, "user", "agent.input",
			proto.AgentInputArgs{Name: developer, Text: "NEVER-DELIVERED"}, nil)
	}()
	waitFor(t, time.Second, "input queued", func() bool { return o.Sup.InputPending(developer) })
	if err := o.Sup.KillAgent(developer, true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "session ended") {
			t.Fatalf("queued input after session end = %v, want session-ended error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued input remained blocked after session ended")
	}
	waitFor(t, time.Second, "pending input cleared", func() bool { return !o.Sup.InputPending(developer) })
	var sent int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_input_sent'`).Scan(&sent); err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatalf("delivery audit count = %d for ended session, want 0", sent)
	}
}

func TestConfigReloadDoesNotReleaseInputDuringWritableInteraction(t *testing.T) {
	o := newOffice(t, map[string]string{"developer": "ready\nsleep|60s\n"})
	developer, err := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "developer ready", func() bool {
		return agentState(t, o, developer) == "working"
	})
	o.Sup.SetInteraction(developer, true)
	o.Sup.RecordUserInput(developer)

	done := make(chan error, 1)
	go func() {
		done <- sockc.Call(o.Sup.SocketPath, "user", "agent.input",
			proto.AgentInputArgs{Name: developer, Text: "STILL-QUEUED-AFTER-RELOAD"}, nil)
	}()
	waitFor(t, time.Second, "input queued", func() bool { return o.Sup.InputPending(developer) })

	reloaded := *o.Sup.Config()
	reloaded.Notifications.InputDebounce = 0
	o.Sup.replaceConfig(&reloaded)
	select {
	case err := <-done:
		t.Fatalf("config reload released input during writable interaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	o.Sup.SetInteraction("", false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued input was not released after leaving writable interaction")
	}
}

func TestQueuedInputRechecksHumanTypingAfterWaitingForInputOwnership(t *testing.T) {
	o := newOffice(t, map[string]string{"developer": "ready\nsleep|60s\n"})
	o.Sup.Cfg.Notifications.InputDebounce = config.Duration(time.Hour)
	developer, err := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "developer ready", func() bool {
		return agentState(t, o, developer) == "working"
	})

	waitingForInputOwnership := make(chan struct{})
	inputOwnershipGranted := make(chan struct{})
	inputDeclined := make(chan struct{})
	var attempts atomic.Int32
	var writes atomic.Int32
	input := &queuedAgentInput{
		caller: "user", detail: "target=" + developer, done: make(chan error, 1),
	}
	o.Sup.sendAgentInput = func(_ *session.Session, _, _ string, ready func() bool) (bool, error) {
		if attempts.Add(1) == 1 {
			close(waitingForInputOwnership)
			<-inputOwnershipGranted
		}
		if !ready() {
			close(inputDeclined)
			return false, nil
		}
		writes.Add(1)
		return true, nil
	}
	o.Sup.mu.Lock()
	o.Sup.pendingAgentInput[developer] = append(o.Sup.pendingAgentInput[developer], input)
	o.Sup.mu.Unlock()
	go o.Sup.flushAgentInput(developer)

	select {
	case <-waitingForInputOwnership:
	case <-time.After(time.Second):
		t.Fatal("queued input never reached the input-ownership boundary")
	}
	o.Sup.SetInteraction(developer, true)
	o.Sup.RecordUserInput(developer)
	close(inputOwnershipGranted)
	select {
	case <-inputDeclined:
	case <-time.After(time.Second):
		t.Fatal("input was not declined after human interaction began")
	}
	if writes.Load() != 0 {
		t.Fatal("queued input wrote after human typing began while it waited for input ownership")
	}
	if !o.Sup.InputPending(developer) {
		t.Fatal("declined input did not remain pending")
	}
	var sent int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_input_sent'`).Scan(&sent); err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatalf("delivery audit count = %d before release, want 0", sent)
	}

	o.Sup.SetInteraction("", false)
	select {
	case err := <-input.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("declined input was not delivered after interaction release")
	}
	if writes.Load() != 1 || attempts.Load() < 2 {
		t.Fatalf("writes=%d attempts=%d, want one write after a declined attempt", writes.Load(), attempts.Load())
	}
}
