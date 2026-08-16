package supervisor

import (
	"testing"
	"time"

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

func TestAgentInputIsAvailableToUserCEOAndFirefighter(t *testing.T) {
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

	for _, caller := range []string{"user", ceo, ff} {
		args := proto.AgentInputArgs{Name: developer, Text: caller, Keys: []string{"enter"}}
		if err := sockc.Call(o.Sup.SocketPath, caller, "agent.input", args, nil); err != nil {
			t.Errorf("%s input: %v", caller, err)
		}
	}
	var events int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_input_sent'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 3 {
		t.Fatalf("input events = %d, want 3", events)
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
