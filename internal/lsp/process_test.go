package lsp

import "testing"

type recordingGroup struct {
	closes int
	forbid bool
	t      *testing.T
}

func (g *recordingGroup) Close() {
	g.closes++
	if g.forbid {
		g.t.Error("signaled a group after its process was reaped")
	}
}
func TestServerStopClosesGroupOnceAndNeverAfterReap(t *testing.T) {
	group := &recordingGroup{t: t}
	canceled := 0
	s := &languageServer{group: group, waited: make(chan struct{}), cancel: func() { canceled++ }}
	// Match the real waiter's ordering: release the group once, then announce
	// that Wait and cleanup completed. Its numeric PID may now be reused.
	s.closeGroup()
	close(s.waited)
	group.forbid = true
	s.stop()
	s.stop()
	if group.closes != 1 || canceled != 1 {
		t.Fatalf("cleanup repeated: closes=%d cancel=%d", group.closes, canceled)
	}
}
