package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// An agent ignores issues of its own worktree only: a working role may ask
// (and learns that an id names nothing there), the CEO and the user have no
// monitored worktree, and an empty id is a bad request.
func TestLSPIgnoreRouteAnswersByRole(t *testing.T) {
	s, _ := engineServer(t, &testRunner{}, fakeProfile(), false)
	for _, role := range []api.Role{api.RoleDeveloper, api.RoleReviewer, api.RolePM} {
		session := liveSession(t, s, role).ID
		rec := socketDo(t, s, "POST", "/api/lsp/ignore", api.LSPIgnoreRequest{ID: "nothing"}, session)
		wantStatus(t, rec.Code, http.StatusNotFound)
		if code := errorCode(t, rec.Body); code != api.CodeNotFound {
			t.Errorf("%s: unknown issue answered %s", role, code)
		}
		rec = socketDo(t, s, "POST", "/api/lsp/ignore", api.LSPIgnoreRequest{}, session)
		wantStatus(t, rec.Code, http.StatusBadRequest)
	}
	ceo := liveSession(t, s, api.RoleCEO).ID
	for who, session := range map[string]string{"ceo": ceo, "user": ""} {
		rec := socketDo(t, s, "POST", "/api/lsp/ignore", api.LSPIgnoreRequest{ID: "x"}, session)
		wantStatus(t, rec.Code, http.StatusForbidden)
		if code := errorCode(t, rec.Body); code != api.CodeForbidden {
			t.Errorf("%s: answered %s", who, code)
		}
	}
}

// The ignore verb is an MCP tool for the working roles, with the same
// answers as the route.
func TestLSPIgnoreIsAnMCPToolForWorkingRoles(t *testing.T) {
	s, _ := engineServer(t, &testRunner{}, fakeProfile(), false)
	dev := liveSession(t, s, api.RoleDeveloper).ID
	ceo := liveSession(t, s, api.RoleCEO).ID
	if !slices.Contains(listToolNames(t, viaSocket(t, s, dev)), "lsp_ignore") {
		t.Fatal("the developer does not see lsp_ignore")
	}
	if slices.Contains(listToolNames(t, viaSocket(t, s, ceo)), "lsp_ignore") {
		t.Fatal("the CEO sees lsp_ignore")
	}
	res, err := callTool(viaSocket(t, s, dev), "lsp_ignore", map[string]any{"id": "nothing"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(toolText(res), "no language-server issue") {
		t.Fatalf("tool answer: error=%v %s", res.IsError, toolText(res))
	}
}

// A GET of /mcp with Accept: text/event-stream as an agent session is its
// notification stream: the session counts as listening while it is open and
// established, and no other session does.
func TestMCPNotificationStreamConfirmsOnlyItsOwnSession(t *testing.T) {
	s, _ := engineServer(t, &testRunner{}, fakeProfile(), false)
	one := liveSession(t, s, api.RoleDeveloper).ID
	two := liveSession(t, s, api.RoleReviewer).ID
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, mcpSocket(t, s), nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(SessionHeader, one)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	rd := bufio.NewReader(resp.Body)
	if line, err := rd.ReadString('\n'); err != nil || !strings.HasPrefix(line, ":") {
		t.Fatalf("opening line %q %v", line, err)
	}
	listeners := s.mcp.listeners
	if !listeners.Confirmed(one) {
		t.Fatal("an established stream does not confirm its session")
	}
	if listeners.Confirmed(two) {
		t.Fatal("another session is confirmed")
	}
	got := make(chan string, 1)
	go func() {
		for {
			line, err := rd.ReadString('\n')
			if err != nil || strings.HasPrefix(line, "event:") {
				got <- line
				return
			}
		}
	}()
	if !listeners.Notify(one, "hello") {
		t.Fatal("a confirmed stream refused a notification")
	}
	if line := <-got; !strings.Contains(line, "event: message") {
		t.Fatalf("the client received %q", line)
	}
	cancel()
	if err := listeners.Wait(t.Context(), one, false); err != nil {
		t.Fatal(err)
	}
}

// A request for a stream without a session of an agent is the SDK's own
// answer, never a listener.
func TestMCPNotificationStreamNeedsAnAgentSession(t *testing.T) {
	s, _ := engineServer(t, &testRunner{}, fakeProfile(), false)
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("a connection without a session opened a stream: %d %s", rec.Code, rec.Body)
	}
}
