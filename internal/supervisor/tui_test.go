package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestTUIShowRequiresUserAndTransitionsAttachedTUI(t *testing.T) {
	o := newOffice(t, map[string]string{"developer": "ready\nsleep|60s\n"})
	developer, err := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "developer ready", func() bool { return agentState(t, o, developer) == "working" })

	states := make(chan [2]string, 2)
	detach := o.Sup.AttachTUI(func(mode, peek string) { states <- [2]string{mode, peek} })
	defer detach()

	if err := sockc.Call(o.Sup.SocketPath, "user", "tui.show", proto.TUIShowArgs{Agent: developer}, nil); err != nil {
		t.Fatal(err)
	}
	if got := <-states; got != [2]string{"peek", developer} {
		t.Fatalf("peek state = %v", got)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "tui.show", proto.TUIShowArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := <-states; got != [2]string{"overview", ""} {
		t.Fatalf("overview state = %v", got)
	}

	if err := sockc.Call(o.Sup.SocketPath, developer, "tui.show", proto.TUIShowArgs{Agent: developer}, nil); err == nil || !strings.Contains(err.Error(), "user") {
		t.Fatalf("non-user tui.show = %v", err)
	}
	if err := db.SetAgentState(o.DB, developer, "dead"); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "tui.show", proto.TUIShowArgs{Agent: developer}, nil); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("dead agent tui.show = %v", err)
	}
}

func TestTUIShowRejectsUnknownAgentWithoutChangingState(t *testing.T) {
	o := newOffice(t, nil)
	states := make(chan [2]string, 1)
	detach := o.Sup.AttachTUI(func(mode, peek string) { states <- [2]string{mode, peek} })
	defer detach()

	if err := sockc.Call(o.Sup.SocketPath, "user", "tui.show", proto.TUIShowArgs{Agent: "missing"}, nil); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("unknown agent tui.show = %v", err)
	}
	select {
	case state := <-states:
		t.Fatalf("unknown agent changed TUI state to %v", state)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTUIShowHeadlessReturnsUnattachedBeforeTargetValidation(t *testing.T) {
	o := newOffice(t, nil)
	if err := sockc.Call(o.Sup.SocketPath, "user", "tui.show", proto.TUIShowArgs{Agent: "missing"}, nil); err == nil || err.Error() != "tui not attached" {
		t.Fatalf("detached tui.show = %v, want tui not attached", err)
	}
}

func TestTUIShowAttachThenDetachReturnsUnattachedBeforeTargetValidation(t *testing.T) {
	o := newOffice(t, nil)
	detach := o.Sup.AttachTUI(func(string, string) { t.Fatal("detached TUI received state") })
	detach()
	if err := sockc.Call(o.Sup.SocketPath, "user", "tui.show", proto.TUIShowArgs{Agent: "missing"}, nil); err == nil || err.Error() != "tui not attached" {
		t.Fatalf("attach-then-detach tui.show = %v, want tui not attached", err)
	}
}

func TestReadOnlyObserverTUIStateSeamReturnsUnattached(t *testing.T) {
	o := newOffice(t, nil)
	if err := o.Sup.SetTUIState("peek", "missing"); err == nil || err.Error() != "tui not attached" {
		t.Fatalf("observer TUI state = %v, want tui not attached", err)
	}
}
