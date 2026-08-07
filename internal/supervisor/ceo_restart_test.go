package supervisor

import (
	"testing"
	"time"
)

// A CEO that cannot start must not be respawned forever: handleDeath
// respawns the CEO unconditionally, so a CEO crashing at startup otherwise
// spins at full speed, creating thousands of agent rows, PTYs and log files.
func TestCEORespawnIsBounded(t *testing.T) {
	o := newOffice(t, map[string]string{})
	// "false" exits immediately without ever calling `omo ready`.
	p := o.Sup.Cfg.Models["ceo"]
	p.Cmd = "false"
	p.Args = nil
	o.Sup.Cfg.Models["ceo"] = p

	if _, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office"); err != nil {
		t.Fatal(err)
	}
	// Wait for omo to give up (it reports that to the user), which is the
	// only state in which the retry count is final.
	waitFor(t, 20*time.Second, "omo gives up on the CEO", func() bool {
		n, _ := o.Sup.Mail.UnreadCount("user")
		return n > 0
	})
	// Nothing may respawn after the give-up.
	time.Sleep(2 * time.Second)
	var total int
	o.DB.QueryRow(`SELECT COUNT(*) FROM agents WHERE role = 'ceo'`).Scan(&total)
	if total > MaxCEORestarts+1 {
		t.Fatalf("CEO respawned %d times, want at most %d", total, MaxCEORestarts+1)
	}
	if total < 2 {
		t.Fatalf("CEO was not retried at all (%d spawns)", total)
	}
	if n, _ := o.Sup.Mail.UnreadCount("user"); n == 0 {
		t.Fatal("giving up on the CEO must be reported to the user")
	}
}
