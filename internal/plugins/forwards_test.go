package plugins

import (
	"context"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"testing"
	"time"
)

type blockedForwardDesk struct {
	ForwardDesk
	entered, release, finished chan struct{}
}

func (d blockedForwardDesk) AddForward(context.Context, string, ForwardAdd) (Forward, error) {
	close(d.entered)
	<-d.release
	close(d.finished)
	return Forward{}, nil
}

func TestForwardCreationCompletesBeforeUnloadCleanup(t *testing.T) {
	e := newEnv(t)
	e.plugin("p", manifestOf(map[string]any{"event": EventManual, "name": "open", "description": "Open", "lua": "open.lua"}), map[string]string{"open.lua": `omo.forward_add{name="tool",port=3456};return "ok"`})
	d := blockedForwardDesk{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	e.m.o.Forwards = d
	cleaned := make(chan bool, 1)
	e.m.o.Unloaded = func(string, string) {
		select {
		case <-d.finished:
			cleaned <- true
		default:
			cleaned <- false
		}
	}
	e.load(enabled("p", nil))
	actionDone := make(chan struct{})
	go func() { defer close(actionDone); _, _ = e.m.Trigger(t.Context(), "p", "open", nil) }()
	<-d.entered
	loaded := make(chan struct{})
	go func() {
		defer close(loaded)
		p := enabled("p", nil)
		p.Enabled = false
		e.m.Load([]api.PluginSettings{p})
	}()
	// The adapter is deliberately delayed past cancellation. Cleanup must wait.
	select {
	case good := <-cleaned:
		close(d.release)
		<-actionDone
		<-loaded
		if !good {
			t.Fatal("cleanup ran before in-flight forward creation")
		}
	case <-time.After(20 * time.Millisecond):
		close(d.release)
		<-actionDone
		<-loaded
		if !<-cleaned {
			t.Fatal("cleanup preceded forward creation")
		}
	}
}
