package company

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "img-src 'self';") {
		t.Fatal("dashboard must allow its locally embedded logos")
	}
	for _, path := range []string{"/assets/xterm.js", "/assets/xterm.css", "/assets/app.js", "/assets/app.css", "/assets/terminal-input.js", "/assets/logo.jpg", "/assets/logo-transparent.png", "/assets/favicon.png"} {
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

func TestBasicAuthProtectsDashboardAndAPI(t *testing.T) {
	projectHome(t)
	s, err := New(Options{MaxAgents: 2, BasicAuth: "operator:secret:with-colon"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })

	for _, path := range []string{"/", "/assets/app.js", "/api/state"} {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic ") {
			t.Fatalf("unauthenticated %s: HTTP %d, challenge %q", path, resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	}

	for _, tc := range []struct {
		user, password string
		want           int
	}{{"operator", "wrong", http.StatusUnauthorized}, {"wrong", "secret:with-colon", http.StatusUnauthorized}, {"operator", "secret:with-colon", http.StatusOK}} {
		req, _ := http.NewRequest("GET", ts.URL+"/api/state", nil)
		req.SetBasicAuth(tc.user, tc.password)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("credentials %q/%q: HTTP %d, want %d", tc.user, tc.password, resp.StatusCode, tc.want)
		}
	}
}

func TestNoOriginCheckAllowsReverseProxyOriginButRetainsHostCheck(t *testing.T) {
	projectHome(t)
	s, err := New(Options{MaxAgents: 2, Unsafe: true, NoOriginCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })

	req, _ := http.NewRequest("GET", ts.URL+"/api/state", nil)
	req.Header.Set("Origin", "https://dashboard.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reverse-proxy origin rejected: HTTP %d", resp.StatusCode)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/state", nil)
	req.Host = "evil.example"
	resp, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("host check was disabled too: HTTP %d", resp.StatusCode)
	}
}

func TestBasicAuthValidation(t *testing.T) {
	projectHome(t)
	for _, options := range []Options{
		{MaxAgents: 1, BasicAuth: "missing-password:"},
		{MaxAgents: 1, BasicAuth: ":missing-user"},
		{MaxAgents: 1, BasicAuth: "no-separator"},
		{MaxAgents: 1, BasicAuth: "user:password", Unsafe: true},
	} {
		if s, err := New(options); err == nil {
			s.Close()
			t.Fatalf("accepted options %+v", options)
		}
	}
}

func TestUnsafeRunPrintsWarningAndPlainURL(t *testing.T) {
	projectHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	if err := RunWithReady(ctx, Options{Listen: "127.0.0.1:0", MaxAgents: 1, Unsafe: true}, &output, func(string) error { cancel(); return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "token authentication is disabled") {
		t.Fatalf("missing unsafe warning: %q", output.String())
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "omo company:") && strings.Contains(line, "#") {
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
	for _, body := range []string{
		`{"action":"trust","path":"/","child_id":"spoof"}`,
		`{"action":"trust","path":"/","source":""}`,
		`{"action":"untrust","path":"/","paths":[]}`,
		`{"action":"create","path":"/","paths":[]}`,
		`{"action":"clone","path":"/"}`,
		`{} {}`,
		`{"action":"remove","path":"/"}`,
	} {
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

func TestAPIProjectReorderReturnsStoredOrder(t *testing.T) {
	s, ts := testServer(t)
	dir := projectHome(t)
	paths := []string{filepath.Join(dir, "one"), filepath.Join(dir, "two"), filepath.Join(dir, "three")}
	for _, path := range paths {
		testOffice(t, path)
	}
	want := []string{paths[2], paths[0], paths[1]}
	body, _ := json.Marshal(map[string]any{"action": "reorder", "paths": want})
	status, data := requestAPI(t, s, ts, "POST", "/api/projects", string(body))
	if status != http.StatusOK {
		t.Fatalf("reorder: HTTP %d %s", status, data)
	}
	var response struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(response.Projects))
	for _, project := range response.Projects {
		got = append(got, project.Path)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("reorder response = %v, want %v", got, want)
	}
	projects, err := Projects()
	if err != nil {
		t.Fatal(err)
	}
	got = got[:0]
	for _, project := range projects {
		got = append(got, project.Path)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("refreshed projects = %v, want %v", got, want)
	}
}

func TestAPIProjectReorderRejectsInvalidRequestsWithoutMutation(t *testing.T) {
	s, ts := testServer(t)
	dir := projectHome(t)
	paths := []string{filepath.Join(dir, "one"), filepath.Join(dir, "two"), filepath.Join(dir, "three")}
	for _, path := range paths {
		testOffice(t, path)
	}
	before, err := Projects()
	if err != nil {
		t.Fatal(err)
	}
	valid := func() []string {
		result := make([]string, 0, len(before))
		for _, project := range before {
			result = append(result, project.Path)
		}
		return result
	}
	for name, paths := range map[string][]string{
		"count mismatch":    {paths[0], paths[1]},
		"duplicate":         {paths[0], paths[0], paths[2]},
		"missing and extra": {paths[0], paths[1], filepath.Join(dir, "other")},
	} {
		t.Run(name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"action": "reorder", "paths": paths})
			status, _ := requestAPI(t, s, ts, "POST", "/api/projects", string(body))
			if status != http.StatusBadRequest {
				t.Fatalf("invalid reorder: HTTP %d", status)
			}
			projects, err := Projects()
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(projects))
			for _, project := range projects {
				got = append(got, project.Path)
			}
			if !slices.Equal(got, valid()) {
				t.Fatalf("invalid reorder mutated projects = %v", got)
			}
		})
	}
	for name, body := range map[string]string{
		"unknown field":       `{"action":"reorder","paths":[],"extra":true}`,
		"incompatible path":   `{"action":"reorder","paths":[],"path":"ignored"}`,
		"incompatible source": `{"action":"reorder","paths":[],"source":"ignored"}`,
	} {
		t.Run(name, func(t *testing.T) {
			status, _ := requestAPI(t, s, ts, "POST", "/api/projects", body)
			if status != http.StatusBadRequest {
				t.Fatalf("accepted incompatible reorder request: HTTP %d", status)
			}
		})
	}
}

func TestAPIProjectReorderRequiresAuthenticationBeforeMutation(t *testing.T) {
	_, ts := testServer(t)
	dir := projectHome(t)
	paths := []string{filepath.Join(dir, "one"), filepath.Join(dir, "two")}
	for _, path := range paths {
		testOffice(t, path)
	}
	body, _ := json.Marshal(map[string]any{"action": "reorder", "paths": []string{paths[1], paths[0]}})
	req, _ := http.NewRequest("POST", ts.URL+"/api/projects", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing capability: HTTP %d", resp.StatusCode)
	}
	projects, err := Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != len(paths) || projects[0].Path != paths[0] || projects[1].Path != paths[1] {
		t.Fatalf("unauthorized request mutated order: %+v", projects)
	}
}

func TestAPIProjectCreateReturnsSetupInstanceAndStateEntry(t *testing.T) {
	s, ts := testServer(t)
	helper := filepath.Join(t.TempDir(), "project-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	oldExecutable := projectExecutable
	projectExecutable = func() (string, error) { return helper, nil }
	t.Cleanup(func() { projectExecutable = oldExecutable })
	destination := filepath.Join(t.TempDir(), "office")
	body, _ := json.Marshal(map[string]string{"action": "create", "path": destination})
	status, data := requestAPI(t, s, ts, "POST", "/api/projects", string(body))
	if status != http.StatusCreated {
		t.Fatalf("create: HTTP %d %s", status, data)
	}
	var info InstanceInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info.ID == "" || info.Path != destination || info.Mode != "setup" || info.State != "running" {
		t.Fatalf("create response: %+v", info)
	}
	recorder := httptest.NewRecorder()
	s.state(recorder, httptest.NewRequest("GET", "/api/state", nil))
	var snapshot struct {
		Instances []InstanceInfo `json:"instances"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].ID != info.ID {
		t.Fatalf("state instances: %+v", snapshot.Instances)
	}
}

func TestAPIProjectSetupFailureRetainsTerminalAndDoesNotTrust(t *testing.T) {
	s, ts := testServer(t)
	helper := filepath.Join(t.TempDir(), "project-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf 'setup output\\n'\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	oldExecutable := projectExecutable
	projectExecutable = func() (string, error) { return helper, nil }
	t.Cleanup(func() { projectExecutable = oldExecutable })
	destination := filepath.Join(t.TempDir(), "office")
	body, _ := json.Marshal(map[string]string{"action": "create", "path": destination})
	status, data := requestAPI(t, s, ts, "POST", "/api/projects", string(body))
	if status != http.StatusCreated {
		t.Fatalf("create: HTTP %d %s", status, data)
	}
	var info InstanceInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	instance := s.instances[info.ID]
	s.mu.Unlock()
	select {
	case <-instance.done:
	case <-time.After(2 * time.Second):
		t.Fatal("setup failure did not finish")
	}
	if got := instance.snapshot().Error; got != "Setup exited with status 7; inspect the terminal output" {
		t.Fatalf("setup error = %q", got)
	}
	projects, err := Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("failed setup was trusted: %+v", projects)
	}
	state := httptest.NewRecorder()
	s.state(state, httptest.NewRequest("GET", "/api/state", nil))
	var snapshot struct {
		Instances []InstanceInfo `json:"instances"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].ID != info.ID {
		t.Fatalf("failed setup missing from state: %+v", snapshot.Instances)
	}
}

func TestProjectSetupEnvironmentStripsCompanyCredentials(t *testing.T) {
	s, ts := testServer(t)
	envFile := filepath.Join(t.TempDir(), "environment")
	helper := filepath.Join(t.TempDir(), "project-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nenv > \"$OMO_TEST_ENV_FILE\"\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_TEST_ENV_FILE", envFile)
	t.Setenv("OMO_CONTROL_URL", "http://127.0.0.1:1")
	t.Setenv("OMO_CONTROL_TOKEN", "secret")
	t.Setenv("OMO_AGENT_ID", "agent")
	t.Setenv("OMO_SOCKET", "socket")
	oldExecutable := projectExecutable
	projectExecutable = func() (string, error) { return helper, nil }
	t.Cleanup(func() { projectExecutable = oldExecutable })
	destination := filepath.Join(t.TempDir(), "office")
	body, _ := json.Marshal(map[string]string{"action": "create", "path": destination})
	status, _ := requestAPI(t, s, ts, "POST", "/api/projects", string(body))
	if status != http.StatusCreated {
		t.Fatalf("create: HTTP %d", status)
	}
	var environment []byte
	var readErr error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		environment, readErr = os.ReadFile(envFile)
		if readErr == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if readErr != nil {
		t.Fatalf("read setup environment: %v", readErr)
	}
	if bytes.Contains(environment, []byte("OMO_CONTROL_")) || bytes.Contains(environment, []byte("OMO_AGENT_ID=")) || bytes.Contains(environment, []byte("OMO_SOCKET=")) {
		t.Fatalf("setup inherited control credentials: %s", environment)
	}
}

func TestAPIProjectUntrust(t *testing.T) {
	t.Run("removes trusted office", func(t *testing.T) {
		s, ts := testServer(t)
		project := testOffice(t, filepath.Join(t.TempDir(), "office"))
		body, _ := json.Marshal(map[string]string{"action": "untrust", "path": project.Path})
		status, data := requestAPI(t, s, ts, "POST", "/api/projects", string(body))
		if status != http.StatusNoContent || len(data) != 0 {
			t.Fatalf("untrust: HTTP %d body %q", status, data)
		}
		projects, err := Projects()
		if err != nil {
			t.Fatal(err)
		}
		if len(projects) != 0 {
			t.Fatalf("project remained trusted: %+v", projects)
		}
	})

	t.Run("refuses owned running instance", func(t *testing.T) {
		s, ts := testServer(t)
		project := testOffice(t, filepath.Join(t.TempDir(), "office"))
		instance := &Instance{info: InstanceInfo{ID: "running", Path: project.Path, Mode: "shell", State: "running"}}
		s.mu.Lock()
		s.instances[instance.info.ID] = instance
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.instances, instance.info.ID)
			s.mu.Unlock()
		}()
		body, _ := json.Marshal(map[string]string{"action": "untrust", "path": project.Path})
		status, data := requestAPI(t, s, ts, "POST", "/api/projects", string(body))
		if status != http.StatusConflict || !bytes.Contains(data, []byte("stop the running instance before removing this office from trust")) {
			t.Fatalf("running untrust: HTTP %d body %q", status, data)
		}
		projects, err := Projects()
		if err != nil {
			t.Fatal(err)
		}
		if len(projects) != 1 || projects[0].Path != project.Path {
			t.Fatalf("running project was untrusted: %+v", projects)
		}
	})

	t.Run("rejects missing capability before mutation", func(t *testing.T) {
		_, ts := testServer(t)
		project := testOffice(t, filepath.Join(t.TempDir(), "office"))
		body, _ := json.Marshal(map[string]string{"action": "untrust", "path": project.Path})
		req, _ := http.NewRequest("POST", ts.URL+"/api/projects", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("missing capability: HTTP %d body %q", resp.StatusCode, data)
		}
		projects, err := Projects()
		if err != nil {
			t.Fatal(err)
		}
		if len(projects) != 1 || projects[0].Path != project.Path {
			t.Fatalf("unauthorized request mutated trust: %+v", projects)
		}
	})
}

func TestOfficeLaunchRequiresExplicitConfirmation(t *testing.T) {
	s, ts := testServer(t)
	status, body := requestAPI(t, s, ts, "POST", "/api/instances", `{"path":"/","mode":"omo"}`)
	if status != http.StatusBadRequest || !bytes.Contains(body, []byte("confirmation required")) {
		t.Fatalf("office launch without confirmation: HTTP %d: %s", status, body)
	}
}

func TestStateHidesRunningOfficeFromLaunchableProjects(t *testing.T) {
	dir := projectHome(t)
	project := testOffice(t, filepath.Join(dir, "office"))
	s, err := New(Options{MaxAgents: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	instance := &Instance{info: InstanceInfo{ID: "running", Path: project.Path, Mode: "omo", State: "running"}}
	s.instances[instance.info.ID] = instance
	defer delete(s.instances, instance.info.ID)

	projects := func() []Project {
		t.Helper()
		recorder := httptest.NewRecorder()
		s.state(recorder, httptest.NewRequest("GET", "/api/state", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("state: HTTP %d: %s", recorder.Code, recorder.Body.String())
		}
		var snapshot struct {
			Projects []Project `json:"projects"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot.Projects
	}
	if got := projects(); len(got) != 0 {
		t.Fatalf("running office remains launchable: %+v", got)
	}
	instance.info.State = "exited"
	if got := projects(); len(got) != 1 || got[0].Path != project.Path {
		t.Fatalf("exited office did not become launchable again: %+v", got)
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
