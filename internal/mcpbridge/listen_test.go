package mcpbridge

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectLogging starts a bridge for session and connects an MCP client
// that collects the log notifications it receives.
func connectLogging(t *testing.T, o *omo, session, version string) (*mcp.ClientSession, chan string, *syncBuffer) {
	t.Helper()
	cmd := bridgeCmd(map[string]string{"OMO_AGENT_ID": session, "OMO_SOCKET": o.socket})
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	got := make(chan string, 8)
	c := mcp.NewClient(&mcp.Implementation{Name: "test-cli", Version: "1"}, &mcp.ClientOptions{
		LoggingMessageHandler: func(_ context.Context, r *mcp.LoggingMessageRequest) {
			if s, ok := r.Params.Data.(string); ok {
				got <- s
			}
		},
	})
	cs, err := c.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatalf("connect: %v\nbridge: %s", err, stderr)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, got, stderr
}

// waitListening waits until omo holds an established stream of session (or
// no longer does). It waits for that state only; the generous limit makes a
// state that never comes fail the test instead of hanging it.
func waitListening(t *testing.T, o *omo, session string, want bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for o.srv.MCPListeners().Confirmed(session) != want {
		if time.Now().After(deadline) {
			t.Fatalf("omo never saw the stream of the session as listening=%v", want)
		}
		runtime.Gosched()
	}
}

// A stdio client that asks for notifications (logging/setLevel) is a
// listener omo confirms through the bridge's own stream; what omo sends
// reaches that client as a log notification.
func TestBridgeForwardsNotificationsOfAListeningClient(t *testing.T) {
	o := newOmo(t)
	id, _ := o.ceoSession()
	for _, v := range []string{"2025-06-18", "2025-11-25"} {
		t.Run("version "+v, func(t *testing.T) {
			cs, got, stderr := connectLogging(t, o, id, v)
			if err := cs.SetLoggingLevel(context.Background(), &mcp.SetLoggingLevelParams{Level: "error"}); err != nil {
				t.Fatalf("logging/setLevel: %v\nbridge: %s", err, stderr)
			}
			waitListening(t, o, id, true)
			if !o.srv.MCPListeners().Notify(id, "issue over stdio") {
				t.Fatal("omo could not notify a confirmed listener")
			}
			if text := <-got; !strings.Contains(text, "issue over stdio") {
				t.Fatalf("the client received %q", text)
			}
			// The client goes away: omo no longer counts the session.
			cs.Close()
			waitListening(t, o, id, false)
			if o.srv.MCPListeners().Notify(id, "after") {
				t.Fatal("a gone listener took a notification")
			}
		})
	}
}

// Using the tools is not listening: a client that never asked for
// notifications is not a confirmed listener.
func TestBridgeClientThatOnlyUsesToolsIsNotListening(t *testing.T) {
	o := newOmo(t)
	id, _ := o.ceoSession()
	b := startBridge(t, map[string]string{"OMO_AGENT_ID": id, "OMO_SOCKET": o.socket}, "2025-06-18")
	b.tools(t)
	if _, isErr := b.call(t, "whoami", map[string]any{}); isErr {
		t.Fatal("whoami failed")
	}
	if o.srv.MCPListeners().Confirmed(id) {
		t.Fatal("a client that only used tools is confirmed as a listener")
	}
}
