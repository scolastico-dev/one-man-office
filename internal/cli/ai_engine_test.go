package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

type seenRequest struct {
	Method, Path, Query, Session, Body string
}

// fakeServer answers every request with answer and records it. It is
// reached through OMO_SOCKET, so `omo ai` talks to it as to the real server.
func fakeServer(t *testing.T, answer string) func() []seenRequest {
	t.Helper()
	isolate(t)
	t.Setenv("OMO_AGENT_ID", "sess-1")
	dir, err := os.MkdirTemp("", "omo-fake")
	if err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(dir, "run")
	if err := os.MkdirAll(run, 0o700); err != nil {
		t.Fatal(err)
	}
	endpoint, _, cleanup, err := transport.Endpoint(run)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []seenRequest
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get(apiclient.SessionHeader), string(body)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() {
		srv.Close()
		cleanup()
		os.RemoveAll(dir)
	})
	t.Setenv("OMO_SOCKET", endpoint)
	return func() []seenRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]seenRequest(nil), seen...)
	}
}

func aiOut(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return execute(context.Background(), append([]string{"ai"}, args...)...)
}

const ticketAnswer = `{"number":5,"type":"task","column":"in_progress","title":"t"}`

func TestAITicketRetry(t *testing.T) {
	seen := fakeServer(t, ticketAnswer)
	out, err := aiOut(t, "ticket", "retry", "5")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, out, "#5 task in_progress t")
	got := seen()
	if len(got) != 1 || got[0].Method != "POST" || got[0].Path != "/api/tickets/5/retry" || got[0].Body != "{}" || got[0].Session != "sess-1" {
		t.Fatalf("requests: %+v", got)
	}
}

func TestAITicketListSupervisorFlag(t *testing.T) {
	seen := fakeServer(t, `{"tickets":[]}`)
	if _, err := aiOut(t, "ticket", "list", "--supervisor", "ceo"); err != nil {
		t.Fatal(err)
	}
	if got := seen(); len(got) != 1 || got[0].Query != "supervisor=ceo" {
		t.Fatalf("requests: %+v", got)
	}
	if _, err := aiOut(t, "ticket", "list", "--supervisor", "dev"); err == nil || !strings.Contains(err.Error(), "--supervisor") {
		t.Fatalf("bad supervisor: %v", err)
	}
	if len(seen()) != 1 {
		t.Fatal("a bad --supervisor reached the server")
	}
}

func TestAITicketCancelDiscardFlag(t *testing.T) {
	seen := fakeServer(t, ticketAnswer)
	if _, err := aiOut(t, "ticket", "cancel", "5", "--discard-uncommitted"); err == nil || !strings.Contains(err.Error(), "--delete-branch") {
		t.Fatalf("without --delete-branch: %v", err)
	}
	if len(seen()) != 0 {
		t.Fatal("the usage error reached the server")
	}
	if _, err := aiOut(t, "ticket", "cancel", "5", "--delete-branch", "--discard-uncommitted"); err != nil {
		t.Fatal(err)
	}
	got := seen()
	if len(got) != 1 || got[0].Path != "/api/tickets/5/cancel" || got[0].Body != `{"delete_branch":true,"discard_uncommitted":true}` {
		t.Fatalf("requests: %+v", got)
	}
}

func TestAIDoneURLAndFailed(t *testing.T) {
	seen := fakeServer(t, ticketAnswer)
	if _, err := aiOut(t, "done", "--url", "https://x"); err != nil {
		t.Fatal(err)
	}
	if _, err := aiOut(t, "done", "--failed", "boom"); err != nil {
		t.Fatal(err)
	}
	got := seen()
	if len(got) != 2 {
		t.Fatalf("requests: %+v", got)
	}
	var first, second api.DoneRequest
	if json.Unmarshal([]byte(got[0].Body), &first) != nil || first != (api.DoneRequest{URL: "https://x"}) ||
		json.Unmarshal([]byte(got[1].Body), &second) != nil || second != (api.DoneRequest{Error: "boom"}) {
		t.Fatalf("bodies: %s and %s", got[0].Body, got[1].Body)
	}
	for _, args := range [][]string{
		{"done", "--url", "https://x", "--approve"},
		{"done", "--failed", "boom", "--reject"},
		{"done", "--url", "https://x", "--failed", "boom"},
		{"done", "--url", "https://x", "some comment"},
		{"done", "--url", ""},
	} {
		if _, err := aiOut(t, args...); err == nil {
			t.Fatalf("%v: no usage error", args)
		}
	}
	if len(seen()) != 2 {
		t.Fatal("a usage error reached the server")
	}
}

func TestAIWaitSendsDuration(t *testing.T) {
	seen := fakeServer(t, `{"reason":"timeout"}`)
	out, err := aiOut(t, "wait", "--timeout", "90s")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, out, "timeout")
	var req api.WaitRequest
	got := seen()
	if len(got) != 1 || json.Unmarshal([]byte(got[0].Body), &req) != nil || time.Duration(req.Timeout) != 90*time.Second {
		t.Fatalf("requests: %+v", got)
	}
}

func TestStopWaitsForHandoffTimeout(t *testing.T) {
	home := isolate(t)
	mustExecute(t, "config", "list")
	if err := os.WriteFile(filepath.Join(home, "settings.yaml"), []byte("stop:\n  handoff_timeout: 5s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var deadline time.Time
	old := stopServer
	stopServer = func(ctx context.Context) error {
		deadline, _ = ctx.Deadline()
		return nil
	}
	t.Cleanup(func() { stopServer = old })
	start := time.Now()
	out := mustExecute(t, "stop")
	if left := deadline.Sub(start); left < 35*time.Second || left > 36*time.Second {
		t.Fatalf("stop deadline is %v away, want about 35s", left)
	}
	contains(t, out, "asking agents for a handoff (up to 5s)...")
	contains(t, out, "omo stopped.")
}
