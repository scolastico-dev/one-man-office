package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
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

// openStream opens the notification stream of session over the socket and
// returns its reader and the token of the probe that came first.
func openNotificationStream(t *testing.T, s *Server, session string) (*bufio.Reader, context.CancelFunc, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, mcpSocket(t, s), nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(SessionHeader, session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	rd := bufio.NewReader(resp.Body)
	var token string
	for token == "" {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("no probe: %v", err)
		}
		if m := regexp.MustCompile("lsp confirm ([0-9a-f]+)`").FindStringSubmatch(line); m != nil {
			token = m[1]
		}
	}
	return rd, cancel, token
}

// A GET of /mcp with Accept: text/event-stream as an agent session is its
// notification stream. It sends a probe; the session counts as listening only
// after its own session returned the probe's token, and no other session does.
func TestMCPNotificationStreamNeedsItsProbeConfirmed(t *testing.T) {
	s, _ := engineServer(t, &testRunner{}, fakeProfile(), false)
	one := liveSession(t, s, api.RoleDeveloper).ID
	two := liveSession(t, s, api.RoleReviewer).ID
	rd, cancel, token := openNotificationStream(t, s, one)
	listeners := s.mcp.listeners
	if listeners.Confirmed(one) || listeners.Notify(one, "too early") {
		t.Fatal("an open stream with an unanswered probe counts as listening")
	}
	// The other session cannot confirm it; a wrong token is refused.
	for _, c := range []struct{ session, token string }{{two, token}, {one, "0000"}} {
		rec := socketDo(t, s, "POST", "/api/lsp/confirm", api.LSPConfirmRequest{Token: c.token}, c.session)
		wantStatus(t, rec.Code, http.StatusNotFound)
	}
	if listeners.Confirmed(one) || listeners.Confirmed(two) {
		t.Fatal("a refused confirmation confirmed a session")
	}
	rec := socketDo(t, s, "POST", "/api/lsp/confirm", api.LSPConfirmRequest{Token: token}, one)
	wantStatus(t, rec.Code, http.StatusOK)
	if !listeners.Confirmed(one) || listeners.Confirmed(two) {
		t.Fatal("the session's own token did not confirm exactly its session")
	}
	got := make(chan string, 1)
	go func() {
		for {
			line, err := rd.ReadString('\n')
			if err != nil || strings.Contains(line, "hello") {
				got <- line
				return
			}
		}
	}()
	if !listeners.Notify(one, "hello") {
		t.Fatal("a confirmed stream refused a notification")
	}
	if line := <-got; !strings.Contains(line, "hello") {
		t.Fatalf("the client received %q", line)
	}
	cancel()
	if err := listeners.Wait(t.Context(), one, false); err != nil {
		t.Fatal(err)
	}
	// A reconnect is unconfirmed until it is confirmed again; the old token is stale.
	_, _, fresh := openNotificationStream(t, s, one)
	if fresh == token || listeners.Confirmed(one) {
		t.Fatal("a new stream inherited the confirmation")
	}
	wantStatus(t, socketDo(t, s, "POST", "/api/lsp/confirm", api.LSPConfirmRequest{Token: token}, one).Code, http.StatusNotFound)
	wantStatus(t, socketDo(t, s, "POST", "/api/lsp/confirm", api.LSPConfirmRequest{Token: fresh}, one).Code, http.StatusOK)
}

func TestLSPConfirmRefusesTheUser(t *testing.T) {
	s, _ := engineServer(t, &testRunner{}, fakeProfile(), false)
	rec := socketDo(t, s, "POST", "/api/lsp/confirm", api.LSPConfirmRequest{Token: "x"}, "")
	wantStatus(t, rec.Code, http.StatusForbidden)
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
