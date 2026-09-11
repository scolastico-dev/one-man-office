package company

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/proto"
)

func TestGlobalPluginTriggerReturnsResultAndRequestID(t *testing.T) {
	root := projectHome(t)
	plugin := filepath.Join(root, "global", "plugins", "report")
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "global", "config.yaml"), []byte(`trusted_offices: []
plugins:
  update_on_start: false
  installed:
    report:
      source: https://example.test/report.git
      enabled: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"name":"report","hooks":[{"event":"manual","name":"run","description":"Run report","roles":["user"],"lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "hook.lua"), []byte(`return {ok = true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{MaxAgents: 1})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })

	status, body := requestAPI(t, s, ts, http.MethodPost, "/api/plugins/report/trigger", `{"action":"run","args":[]}`)
	if status != http.StatusOK {
		t.Fatalf("trigger: HTTP %d %s", status, body)
	}
	var response struct {
		RequestID int64          `json:"request_id"`
		Result    map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.RequestID < 1 || response.Result["ok"] != true {
		t.Fatalf("trigger response = %+v", response)
	}
}

func TestGlobalPluginTriggerDeniesDisallowedUserRoleBeforeHook(t *testing.T) {
	root := projectHome(t)
	plugin := filepath.Join(root, "global", "plugins", "restricted")
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "global", "config.yaml"), []byte(`trusted_offices: []
plugins:
  update_on_start: false
  installed:
    restricted:
      source: https://example.test/restricted.git
      enabled: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"name":"restricted","hooks":[{"event":"manual","name":"run","description":"Restricted action","roles":["ceo"],"lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "hook.lua"), []byte(`omo.local_set("ran", true)`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{MaxAgents: 1})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })

	status, body := requestAPI(t, s, ts, http.MethodPost, "/api/plugins/restricted/trigger", `{"action":"run","args":[]}`)
	if status != http.StatusForbidden {
		t.Fatalf("denied trigger: HTTP %d %s, want 403", status, body)
	}
	var ran int
	if err := s.pluginDB.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE plugin='restricted' AND key='ran'`).Scan(&ran); err != nil {
		t.Fatal(err)
	}
	if ran != 0 {
		t.Fatalf("disallowed global trigger reached hook: %d storage rows", ran)
	}
	var audited int
	if err := s.pluginDB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind LIKE 'plugin_manual_%'`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 0 {
		t.Fatalf("disallowed global trigger created %d audit events", audited)
	}
}

func TestGlobalPluginTriggerHonorsBasicAuthentication(t *testing.T) {
	root := projectHome(t)
	plugin := filepath.Join(root, "global", "plugins", "basic")
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "global", "config.yaml"), []byte(`trusted_offices: []
plugins:
  update_on_start: false
  installed:
    basic:
      source: https://example.test/basic.git
      enabled: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"name":"basic","hooks":[{"event":"manual","name":"run","description":"Basic action","roles":["user"],"lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "hook.lua"), []byte(`return {ok = true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{MaxAgents: 1, BasicAuth: "operator:secret"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })
	for _, tc := range []struct {
		name     string
		user     string
		password string
		want     int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", user: "operator", password: "wrong", want: http.StatusUnauthorized},
		{name: "valid", user: "operator", password: "secret", want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/plugins/basic/trigger", strings.NewReader(`{"action":"run","args":[]}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if tc.user != "" {
				req.SetBasicAuth(tc.user, tc.password)
			}
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("HTTP %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestGlobalPluginTriggerEnforcesPerPluginAdmission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-specific")
	}
	root := projectHome(t)
	plugin := filepath.Join(root, "global", "plugins", "busy")
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "global", "config.yaml"), []byte(`trusted_offices: []
plugins:
  update_on_start: false
  installed:
    busy:
      source: https://example.test/busy.git
      enabled: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"name":"busy","hooks":[{"event":"manual","name":"run","description":"Busy action","roles":["user"],"command":["sh","-c","sleep 1; printf '{\"ok\":true}'"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{MaxAgents: 1})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })
	first := make(chan int, 1)
	go func() {
		status, _ := requestAPI(t, s, ts, http.MethodPost, "/api/plugins/busy/trigger", `{"action":"run","args":[]}`)
		first <- status
	}()
	deadline := time.Now().Add(time.Second)
	for {
		var count int
		if err := s.pluginDB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_requested'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first trigger did not enter admission")
		}
		time.Sleep(time.Millisecond)
	}
	status, body := requestAPI(t, s, ts, http.MethodPost, "/api/plugins/busy/trigger", `{"action":"run","args":[]}`)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "already running") {
		t.Fatalf("overlapping trigger: HTTP %d %s", status, body)
	}
	if status := <-first; status != http.StatusOK {
		t.Fatalf("first trigger: HTTP %d", status)
	}
}

func TestGlobalPluginTriggerRequiresCapabilityAndValidJSON(t *testing.T) {
	projectHome(t)
	s, ts := testServer(t)
	for _, body := range []string{`{}`, `{"action":3}`, `{"action":"run","args":"bad"}`} {
		status, _ := requestAPI(t, s, ts, http.MethodPost, "/api/plugins/missing/trigger", body)
		if status != http.StatusBadRequest {
			t.Fatalf("body %s: HTTP %d, want 400", body, status)
		}
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/plugins/missing/trigger", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing capability: HTTP %d", resp.StatusCode)
	}
}

func TestInstancePluginTriggerForwardsUserAndReturnsRequestID(t *testing.T) {
	projectHome(t)
	s, ts := testServer(t)
	officeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(officeDir, ".omo"), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(officeDir, "plugin.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.WriteFile(filepath.Join(officeDir, office.LockPath), []byte(listener.Addr().String()), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan proto.Request, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var request proto.Request
				if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&request); err == nil && request.Verb == "plugin.trigger" {
					requests <- request
					_ = json.NewEncoder(conn).Encode(proto.Response{OK: true, Data: json.RawMessage(`{"request_id":91,"result":null}`)})
				}
			}()
		}
	}()
	s.instances["office-1"] = &Instance{info: InstanceInfo{ID: "office-1", Path: officeDir, Mode: "omo", State: "running", Started: time.Now()}}
	t.Cleanup(func() { s.mu.Lock(); delete(s.instances, "office-1"); s.mu.Unlock() })

	status, body := requestAPI(t, s, ts, http.MethodPost, "/api/instances/office-1/trigger", `{"plugin":"filebrowser","action":"download","args":["/tmp/a"]}`)
	if status != http.StatusOK {
		t.Fatalf("instance trigger: HTTP %d %s", status, body)
	}
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response["request_id"] != float64(91) {
		t.Fatalf("instance response = %s", body)
	}
	select {
	case request := <-requests:
		if request.AgentID != "user" {
			t.Fatalf("forwarded caller = %q", request.AgentID)
		}
		var args proto.PluginTriggerArgs
		if err := json.Unmarshal(request.Args, &args); err != nil || args.Name != "filebrowser" || args.Action != "download" {
			t.Fatalf("forwarded args = %s: %v", request.Args, err)
		}
	case <-time.After(time.Second):
		t.Fatal("socket request was not forwarded")
	}
}

func TestInstancePluginTriggerReturnsBeforeOfficeHookCompletes(t *testing.T) {
	projectHome(t)
	s, ts := testServer(t)
	officeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(officeDir, ".omo"), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(officeDir, "plugin.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	if err := os.WriteFile(filepath.Join(officeDir, office.LockPath), []byte(listener.Addr().String()), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan proto.Request, 1)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				var request proto.Request
				if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&request); err != nil {
					return
				}
				if request.Verb != "plugin.trigger" {
					return
				}
				var args proto.PluginTriggerArgs
				if err := json.Unmarshal(request.Args, &args); err != nil || !args.Async {
					return
				}
				requests <- request
				_ = json.NewEncoder(conn).Encode(proto.Response{OK: true, Data: json.RawMessage(`{"request_id":92}`)})
			}()
		}
	}()
	s.instances["office-1"] = &Instance{info: InstanceInfo{ID: "office-1", Path: officeDir, Mode: "omo", State: "running", Started: time.Now()}}
	t.Cleanup(func() { s.mu.Lock(); delete(s.instances, "office-1"); s.mu.Unlock() })

	started := time.Now()
	status, body := requestAPI(t, s, ts, http.MethodPost, "/api/instances/office-1/trigger", `{"plugin":"filebrowser","action":"download","args":["/tmp/a"]}`)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("instance trigger waited %s for the office hook", elapsed)
	}
	if status != http.StatusOK {
		t.Fatalf("instance trigger: HTTP %d %s", status, body)
	}
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response["request_id"] != float64(92) {
		t.Fatalf("instance response = %s", body)
	}
	select {
	case request := <-requests:
		if request.AgentID != "user" {
			t.Fatalf("forwarded caller = %q", request.AgentID)
		}
	case <-time.After(time.Second):
		t.Fatal("async socket request was not forwarded")
	}
}

func TestInstancePluginTriggerRejectsUnavailableInstances(t *testing.T) {
	projectHome(t)
	s, ts := testServer(t)
	for _, id := range []string{"missing", "shell", "exited", "unready"} {
		if id == "shell" || id == "exited" {
			s.instances[id] = &Instance{info: InstanceInfo{ID: id, Path: t.TempDir(), Mode: map[string]string{"shell": "shell", "exited": "omo"}[id], State: map[string]string{"shell": "running", "exited": "exited"}[id], Started: time.Now()}}
		} else if id == "unready" {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, office.LockPath), []byte(filepath.Join(dir, "missing.sock")), 0o600); err != nil {
				t.Fatal(err)
			}
			s.instances[id] = &Instance{info: InstanceInfo{ID: id, Path: dir, Mode: "omo", State: "running", Started: time.Now()}}
		}
		status, _ := requestAPI(t, s, ts, http.MethodPost, "/api/instances/"+id+"/trigger", `{"plugin":"filebrowser","action":"download","args":[]}`)
		if status != http.StatusConflict {
			t.Fatalf("instance %s: HTTP %d, want 409", id, status)
		}
	}
	t.Cleanup(func() {
		s.mu.Lock()
		for _, id := range []string{"shell", "exited", "unready"} {
			delete(s.instances, id)
		}
		s.mu.Unlock()
	})
}

func TestInstancePluginTriggerMapsSocketTimeoutAndError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wantStatus int
		hold       bool
	}{
		{name: "timeout", wantStatus: http.StatusGatewayTimeout, hold: true},
		{name: "socket error", wantStatus: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projectHome(t)
			s, ts := testServer(t)
			officeDir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(officeDir, ".omo"), 0o700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", filepath.Join(officeDir, "plugin.sock"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				listener.Close()
				s.mu.Lock()
				delete(s.instances, "office-1")
				s.mu.Unlock()
				s.Close()
				ts.Close()
			})
			if err := os.WriteFile(filepath.Join(officeDir, office.LockPath), []byte(listener.Addr().String()), 0o600); err != nil {
				t.Fatal(err)
			}
			go func() {
				var connections int
				for {
					conn, acceptErr := listener.Accept()
					if acceptErr != nil {
						return
					}
					connections++
					if connections == 1 {
						_ = conn.Close()
						continue
					}
					if tc.hold {
						time.Sleep(4 * time.Second)
						_ = conn.Close()
						continue
					}
					var request proto.Request
					_ = json.NewDecoder(bufio.NewReader(conn)).Decode(&request)
					_ = json.NewEncoder(conn).Encode(proto.Response{Error: "hook failed"})
					_ = conn.Close()
				}
			}()
			s.instances["office-1"] = &Instance{info: InstanceInfo{ID: "office-1", Path: officeDir, Mode: "omo", State: "running", Started: time.Now()}}
			status, body := requestAPI(t, s, ts, http.MethodPost, "/api/instances/office-1/trigger", `{"plugin":"filebrowser","action":"download","args":[]}`)
			if status != tc.wantStatus {
				t.Fatalf("socket %s: HTTP %d %s, want %d", tc.name, status, body, tc.wantStatus)
			}
		})
	}
}
