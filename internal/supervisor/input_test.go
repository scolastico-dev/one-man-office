package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/proto"
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
