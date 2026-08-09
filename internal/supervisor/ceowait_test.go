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

func TestSmokeAlarmMayNotWait(t *testing.T) {
	o := newOffice(t, map[string]string{"smokealarm": "ready\nsleep|30s\n"})
	smoke, err := o.Sup.Spawn("smokealarm", "smokealarm", 0, o.Dir, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "smoke alarm working", func() bool {
		return agentState(t, o, smoke) == "working"
	})

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- sockc.Call(o.Sup.SocketPath, smoke, "wait", nil, new(proto.WaitResponse))
	}()
	waitFor(t, 5*time.Second, "smoke alarm wait request resolved or parked", func() bool {
		select {
		case err = <-waitResult:
			return true
		default:
			if agentState(t, o, smoke) == "waiting" {
				o.Sup.WakeAgent(smoke)
				err = <-waitResult
				return true
			}
			return false
		}
	})
	if err == nil {
		t.Fatal("a smoke alarm must not be allowed to park in omo wait")
	}
	if !strings.Contains(err.Error(), "must not wait") || !strings.Contains(err.Error(), "omo done") {
		t.Fatalf("error should reject waiting and direct the smoke alarm to omo done, got: %v", err)
	}
	if got := agentState(t, o, smoke); got != "working" {
		t.Fatalf("smoke alarm state = %q, want working", got)
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
