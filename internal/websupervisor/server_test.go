package websupervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	projectHome(t)
	s, err := New(Options{MaxAgents: 2})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })
	return s, ts
}

func requestAPI(t *testing.T, s *Server, ts *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestDashboardRejectsMissingCapabilityCrossOriginAndRebinding(t *testing.T) {
	s, ts := testServer(t)
	for _, tc := range []struct{ token, origin, host string }{
		{}, {token: s.token, origin: "https://evil.example"}, {token: s.token, host: "evil.example"},
	} {
		req, _ := http.NewRequest("GET", ts.URL+"/api/state", nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		if tc.host != "" {
			req.Host = tc.host
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Fatalf("accepted %+v", tc)
		}
	}
	status, body := requestAPI(t, s, ts, "GET", "/api/state", "")
	if status != 200 || !bytes.Contains(body, []byte(`"max_agents":2`)) {
		t.Fatalf("state: %d %s", status, body)
	}
	resp, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || bytes.Contains(html, []byte(s.token)) || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("unsafe dashboard document")
	}
	for _, path := range []string{"/assets/xterm.js", "/assets/xterm.css", "/assets/app.js"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || len(data) < 100 {
			t.Fatalf("asset missing: %s", path)
		}
	}
}

func TestUnsafeDashboardBypassesCapabilityButRetainsOriginChecks(t *testing.T) {
	projectHome(t)
	s, err := New(Options{MaxAgents: 2, Unsafe: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })

	req, _ := http.NewRequest("GET", ts.URL+"/api/state", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unsafe request without token: HTTP %d", resp.StatusCode)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/state", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unsafe cross-origin request: HTTP %d", resp.StatusCode)
	}
}

func TestUnsafeRunPrintsWarningAndPlainURL(t *testing.T) {
	projectHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	if err := Run(ctx, Options{Listen: "127.0.0.1:0", MaxAgents: 1, Unsafe: true}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "token authentication is disabled") {
		t.Fatalf("missing unsafe warning: %q", output.String())
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "omo supervisor:") && strings.Contains(line, "#") {
			t.Fatalf("unsafe access URL still contains a capability fragment: %q", line)
		}
	}
}

func TestAPIProjectTrustAndStrictRequests(t *testing.T) {
	s, ts := testServer(t)
	destination := filepath.Join(t.TempDir(), "office")
	body, _ := json.Marshal(map[string]string{"action": "create", "path": destination})
	status, data := requestAPI(t, s, ts, "POST", "/api/projects", string(body))
	if status != 201 {
		t.Fatalf("create: %d %s", status, data)
	}
	for _, body := range []string{`{"action":"trust","path":"/","child_id":"spoof"}`, `{} {}`, `{"action":"remove","path":"/"}`} {
		status, _ := requestAPI(t, s, ts, "POST", "/api/projects", body)
		if status < 400 {
			t.Fatalf("accepted %s", body)
		}
	}
	status, _ = requestAPI(t, s, ts, "POST", "/api/instances", `{"path":"/","mode":"omo"}`)
	if status < 400 {
		t.Fatal("launched untrusted office")
	}
	status, _ = requestAPI(t, s, ts, "POST", "/api/instances", `{"path":"/","mode":"exec","command":"sh"}`)
	if status < 400 {
		t.Fatal("accepted public executable field")
	}
}

func TestWebsocketCannotUseCookieOrQueryForAuthorization(t *testing.T) {
	s, ts := testServer(t)
	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = "/api/instances/missing/terminal"
	u.RawQuery = "token=" + s.token
	conn, resp, err := websocket.Dial(context.Background(), u.String(), nil)
	if conn != nil {
		conn.CloseNow()
	}
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query token accepted: %v %+v", err, resp)
	}
}
