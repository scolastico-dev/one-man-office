package supervisor

import (
	"strings"
	"testing"
)

func TestHardUsageStopPublishesExitReasonBeforeSignal(t *testing.T) {
	o := newOffice(t, map[string]string{})
	o.Sup.beginHardUsageStop("all providers reached 90%")
	select {
	case <-o.Sup.EmergencyStop():
	default:
		t.Fatal("hard usage stop did not signal office exit")
	}
	if reason := o.Sup.ExitReason(); !strings.Contains(reason, "all providers reached 90%") {
		t.Fatalf("exit reason = %q", reason)
	}
}
