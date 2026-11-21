package mcpbridge

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectLogging starts a bridge for session and connects an MCP client
// that collects the log notifications it receives.
func connectLogging(t *testing.T, o *omo, session, version string, env map[string]string) (*mcp.ClientSession, chan string, *syncBuffer) {
	t.Helper()
	all := map[string]string{"OMO_AGENT_ID": session, "OMO_SOCKET": o.socket}
	if env != nil {
		all = map[string]string{"OMO_AGENT_ID": session}
		for k, v := range env {
			all[k] = v
		}
	}
	cmd := bridgeCmd(all)
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
// no longer does): for that change itself, until the test ends.
func waitListening(t *testing.T, o *omo, session string, want bool) {
	t.Helper()
	if err := o.srv.MCPListeners().Wait(t.Context(), session, want); err != nil {
		t.Fatalf("omo never saw the stream of the session as listening=%v: %v", want, err)
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
			cs, got, stderr := connectLogging(t, o, id, v, nil)
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

// omo restarting ends the stream; the bridge opens it again on the same
// home, so the listening client is confirmed again and receives what omo
// sends next.
func TestBridgeListenerReturnsAfterOmoRestarts(t *testing.T) {
	o := newOmo(t)
	id, _ := o.ceoSession()
	cs, got, stderr := connectLogging(t, o, id, "2025-06-18", map[string]string{"OMO_HOME": o.home})
	if err := cs.SetLoggingLevel(context.Background(), &mcp.SetLoggingLevelParams{Level: "error"}); err != nil {
		t.Fatalf("logging/setLevel: %v\nbridge: %s", err, stderr)
	}
	waitListening(t, o, id, true)
	o.close()
	o.start()
	waitListening(t, o, id, true)
	if !o.srv.MCPListeners().Notify(id, "after the restart") {
		t.Fatal("the restarted omo could not notify the listener")
	}
	if text := <-got; !strings.Contains(text, "after the restart") {
		t.Fatalf("the client received %q", text)
	}
}
