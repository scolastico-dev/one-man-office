package company

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
