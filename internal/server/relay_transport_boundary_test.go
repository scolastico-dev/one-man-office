package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

func relayChunkedRaw(t *testing.T, u *relayUI, method, path, contentType string, data []byte, trailer string) *http.Response {
	t.Helper()
	c, e := u.dial(context.Background(), "tcp", "")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	request := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: omo-relay.invalid\r\nContent-Type: %s\r\nTransfer-Encoding: chunked\r\nOmo-Image-Name: trailer.png\r\n\r\n%x\r\n%s\r\n0\r\n%s\r\n", method, path, contentType, len(data), data, trailer)
	if _, e := io.WriteString(c, request); e != nil {
		t.Fatal(e)
	}
	response, e := http.ReadResponse(bufio.NewReader(c), nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}
func TestRelayUndeclaredTrailersCannotReachMutations(t *testing.T) {
	s, origin := relayFixture(t)
	u := newRelayUI(t, s, origin, nil)
	for _, trailer := range []string{"X-Omo-Session:\r\n", "pRoXy-AuThOrIzAtIoN: secret\r\n", "Cookie: identity=secret\r\n", "X-Forwarded-User: admin\r\n", "Unrelated: value\r\n"} {
		response := relayChunkedRaw(t, u, "PUT", "/api/server/paused", "application/json", []byte(`{"paused":true}`), trailer)
		wantStatus(t, response.StatusCode, 400)
		io.Copy(io.Discard, response.Body)
		paused, e := s.board.Paused(context.Background())
		if e != nil || paused {
			t.Fatal("trailer-bearing mutation committed", paused, e)
		}
	}
	// DELETE does not read its body. Admission must reject before deleting.
	n := userTicket(t, s)
	pngData := testPNG(t)
	request := uploadRequest(t, "http://omo-relay.invalid", n, "keep.png", pngData)
	response, e := u.client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	wantStatus(t, response.StatusCode, 201)
	var stored api.Image
	relayJSON(t, response, &stored)
	response = relayChunkedRaw(t, u, "DELETE", "/api/images/"+stored.ID, "application/json", []byte(`{}`), "X-Omo-Session:\r\n")
	wantStatus(t, response.StatusCode, 400)
	io.Copy(io.Discard, response.Body)
	response = relayRequest(t, u.client(), "GET", "/api/images/"+stored.ID+"/content", nil, nil)
	wantStatus(t, response.StatusCode, 200)
	response.Body.Close()
	// Raw uploads keep their streaming path but may not commit after a trailer.
	response = relayChunkedRaw(t, u, "POST", fmt.Sprintf("/api/tickets/%d/images", n), "application/octet-stream", pngData, "Authorization: Bearer forged\r\n")
	wantStatus(t, response.StatusCode, 400)
	io.Copy(io.Discard, response.Body)
	response = relayRequest(t, u.client(), "GET", fmt.Sprintf("/api/tickets/%d/images", n), nil, nil)
	var list []api.Image
	relayJSON(t, response, &list)
	if len(list) != 1 {
		t.Fatal("trailer upload persisted", len(list))
	}
	// A no-decode POST must not get as far as starting profile detection.
	calls := 0
	s.opts.DetectAgents = func(string) (string, error) { calls++; return "", fmt.Errorf("absent") }
	response = relayChunkedRaw(t, u, "POST", "/api/agent-setup/detect", "application/json", []byte(`{}`), "X-Omo-Role: user\r\n")
	wantStatus(t, response.StatusCode, 400)
	io.Copy(io.Discard, response.Body)
	if calls != 0 {
		t.Fatal("trailer-bearing POST reached detection")
	}
	// Valid chunked JSON still follows its ordinary decoder and commit rules.
	response = relayChunkedRaw(t, u, "PUT", "/api/server/paused", "application/json", []byte(`{"paused":true}`), "")
	wantStatus(t, response.StatusCode, 200)
	io.Copy(io.Discard, response.Body)
}
func TestRelayApplicationInitializedBeforeAdmission(t *testing.T) {
	called := false
	s, e := newServer(Options{Layout: testHome(t), Config: testConfig(), beforeRelayStart: func(s *Server) {
		called = true
		r := httptest.NewRequest("GET", "/api/mcp/servers", nil)
		r = withRequestContext(r, board.User, s.log)
		w := httptest.NewRecorder()
		if s.mcpRelayOrRefuse(w, r) != nil {
			w.WriteHeader(http.StatusOK)
		}
		if w.Code != 200 {
			t.Errorf("application exposed before MCP initialization: %d %s", w.Code, bytes.TrimSpace(w.Body.Bytes()))
		}
	}}, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if !called {
		t.Fatal("startup admission boundary not reached")
	}
}

func TestRelayRejectsUpstreamOAuthAdmission(t *testing.T) {
	s, origin := relayFixture(t)
	u := newRelayUI(t, s, origin, nil)
	for _, action := range []string{"connect", "disconnect"} {
		response := relayRequest(t, u.client(), "POST", "/api/mcp/servers/example/"+action, strings.NewReader(`{}`), http.Header{"Content-Type": {"application/json"}})
		wantStatus(t, response.StatusCode, 403)
		response.Body.Close()
	}
}
