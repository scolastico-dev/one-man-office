package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

// The CEO's session is the user's terminal — it is the TUI's home screen.
// `omo wait` blocks the CLI reading stdin, so a waiting CEO cannot be typed
// to at all. Like the routing matrix, this is enforced, not merely prompted.
func TestCEOMayNotWait(t *testing.T) {
	o := newOffice(t, map[string]string{"ceo": "ready\nsleep|30s\n"})
	ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "ceo working", func() bool { return agentState(t, o, ceo) == "working" })

	err = sockc.Call(o.Sup.SocketPath, ceo, "wait", nil, new(proto.WaitResponse))
	if err == nil {
		t.Fatal("the CEO must not be allowed to park in omo wait")
	}
	if !strings.Contains(err.Error(), "user") {
		t.Fatalf("the error should explain why (the user types here), got: %v", err)
	}
	// It must stay responsive, not be parked or killed.
	if got := agentState(t, o, ceo); got != "working" {
		t.Fatalf("ceo state = %q, want working", got)
	}
}

func TestOtherRolesMayStillWait(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "sit tight")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "freelancer parked", func() bool { return agentState(t, o, name) == "waiting" })
	o.Sup.WakeAgent(name)
}
