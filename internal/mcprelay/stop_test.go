package mcprelay

import (
	"os"
	"testing"
	"time"
)

// Stopping a process that does not end, also when its group cannot kill
// it, returns after a bounded wait instead of hanging every stop.
func TestStoppingAProcessThatDoesNotEndIsBounded(t *testing.T) {
	f := newFixture(t, func(t *tunables) { t.stopWait = 50 * time.Millisecond })
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	t.Cleanup(func() { inR.Close(); outW.Close() })
	i := &instance{pool: f.pool, st: &serverState{redact: &redactor{}}}
	i.proc = &process{group: &processGroup{}, stdin: inW, stdout: outR, exited: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		i.closeProcess()
		close(done)
	}()
	<-done
}
