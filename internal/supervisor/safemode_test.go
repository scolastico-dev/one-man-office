package supervisor

import (
	"errors"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestSafeModeAllowsOnlyCEOUntilUserResumes(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":        "ready\nsleep|60s\n",
		"freelancer": "ready\nsleep|60s\n",
	})
	o.Sup.EnterSafeMode()

	ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "safe mode")
	if err != nil {
		t.Fatalf("spawn CEO: %v", err)
	}
	waitFor(t, 5*time.Second, "CEO ready", func() bool { return agentState(t, o, ceo) == "working" })

	for _, role := range []string{"product_manager", "developer", "reviewer", "freelancer", "smokealarm", "firefighter"} {
		if _, err := o.Sup.Spawn(role, role, 0, o.Dir, "blocked"); !errors.Is(err, ErrSpawningHalted) {
			t.Errorf("spawn %s error = %v, want ErrSpawningHalted", role, err)
		}
	}

	if err := sockc.Call(o.Sup.SocketPath, "user", "office.resume-spawns", nil, nil); err != nil {
		t.Fatalf("user resume: %v", err)
	}
	if o.Sup.SafeMode() {
		t.Fatal("safe mode remained active after resume")
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "allowed"); err != nil {
		t.Fatalf("spawn after resume: %v", err)
	}
}
