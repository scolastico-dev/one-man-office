package sockd

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func startServer(t *testing.T, auth AuthFunc) (*Server, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "omo.sock")
	srv := New(sock, auth)
	srv.Handle("echo", func(agentID string, args json.RawMessage) (any, error) {
		var in map[string]string
		json.Unmarshal(args, &in)
		return map[string]string{"agent": agentID, "text": in["text"]}, nil
	})
	srv.Handle("boom", func(string, json.RawMessage) (any, error) {
		return nil, errors.New("kaput")
	})
	go srv.ListenAndServe()
	t.Cleanup(func() { srv.Close() })
	// Wait for the socket file to exist.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := sockc.Call(sock, "x", "echo", map[string]string{"text": "up"}, nil); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return srv, sock
}

func TestRoundTrip(t *testing.T) {
	_, sock := startServer(t, nil)
	var out map[string]string
	if err := sockc.Call(sock, "developer-jason", "echo", map[string]string{"text": "hi"}, &out); err != nil {
		t.Fatal(err)
	}
	if out["agent"] != "developer-jason" || out["text"] != "hi" {
		t.Fatalf("out = %v", out)
	}
}

func TestHandlerErrorPropagates(t *testing.T) {
	_, sock := startServer(t, nil)
	err := sockc.Call(sock, "a", "boom", nil, nil)
	if err == nil || err.Error() != "kaput" {
		t.Fatalf("err = %v, want kaput", err)
	}
}

func TestUnknownVerbRejected(t *testing.T) {
	_, sock := startServer(t, nil)
	if err := sockc.Call(sock, "a", "nope", nil, nil); err == nil {
		t.Fatal("unknown verb must error")
	}
}

func TestAuthRejects(t *testing.T) {
	_, sock := startServer(t, func(agentID, verb string) error {
		if agentID != "developer-jason" {
			return errors.New("unknown OMO_AGENT_ID")
		}
		return nil
	})
	if err := sockc.Call(sock, "ghost", "echo", nil, nil); err == nil {
		t.Fatal("auth must reject unknown agent")
	}
	if err := sockc.Call(sock, "developer-jason", "echo", map[string]string{"text": "x"}, nil); err != nil {
		t.Fatalf("auth must allow known agent: %v", err)
	}
}
