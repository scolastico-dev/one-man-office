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

func TestUsageSafeShutdownReasonCannotBeOverwritten(t *testing.T) {
	o := newOffice(t, nil)
	const usageReason = "every configured provider reached the usage ceiling"
	o.Sup.beginSoftUsageShutdown(usageReason)
	if err := o.Sup.beginSafeShutdown("user", "manual request"); err != nil {
		t.Fatal(err)
	}
	if got := o.Sup.ExitReason(); got != usageReason {
		t.Fatalf("exit reason = %q, want usage reason", got)
	}
	snapshot := o.Sup.PluginSnapshot()
	if got, ok := snapshot["shutdown_in_progress"].(bool); !ok || !got {
		t.Fatalf("shutdown_in_progress = %#v, want true", snapshot["shutdown_in_progress"])
	}
}
