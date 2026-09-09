package websupervisor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupervisorLoadsGlobalBrowserExtensionAndStartupHook(t *testing.T) {
	root := projectHome(t)
	plugin := filepath.Join(root, "global", "plugins", "dashboard")
	if err := os.MkdirAll(filepath.Join(plugin, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"dashboard","hooks":[{"event":"on_supervisor_startup","lua":"startup.lua"},{"event":"on_supervisor_load","javascript":"web/main.js","files":["web/theme.css"]}]}`
	for path, content := range map[string]string{
		"plugin.json": manifest, "startup.lua": `omo.local_set("started", true)`,
		"web/main.js": `window.extensionLoaded=true`, "web/theme.css": `.extension{color:blue}`, "private.txt": "secret",
	} {
		if err := os.WriteFile(filepath.Join(plugin, filepath.FromSlash(path)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(Options{MaxAgents: 2})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	// Set the authority before serving so Host validation sees the final listener.
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })
	status, body := requestAPI(t, s, ts, "GET", "/api/extensions", "")
	if status != http.StatusOK || !bytes.Contains(body, []byte(`"plugin":"dashboard"`)) || !bytes.Contains(body, []byte("web/main.js")) {
		t.Fatalf("extensions: HTTP %d %s", status, body)
	}
	var extensions []clientExtension
	if err := json.Unmarshal(body, &extensions); err != nil || len(extensions) != 1 {
		t.Fatalf("decode extensions: %+v %v", extensions, err)
	}
	resp, err := ts.Client().Do(authenticatedRequest(t, s, ts.URL+extensions[0].Javascript))
	if err != nil {
		t.Fatal(err)
	}
	javascript, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(javascript, []byte("extensionLoaded")) {
		t.Fatalf("javascript: HTTP %d %s", resp.StatusCode, javascript)
	}
	resp, err = ts.Client().Get(ts.URL + extensions[0].Javascript)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("script-tag request without bearer token: HTTP %d", resp.StatusCode)
	}
	status, _ = requestAPI(t, s, ts, "GET", "/plugins/dashboard/files/private.txt", "")
	if status != http.StatusNotFound {
		t.Fatalf("undeclared file: HTTP %d", status)
	}
	var stored string
	if err := s.pluginDB.QueryRow(`SELECT value FROM plugin_storage WHERE scope='local' AND plugin='dashboard' AND key='started'`).Scan(&stored); err != nil || stored != "true" {
		t.Fatalf("startup state = %q, %v", stored, err)
	}
}

func authenticatedRequest(t *testing.T, s *Server, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	return req
}

func TestExecuteStreamsOutputInHomeAndRejectsUntrustedCwd(t *testing.T) {
	root := projectHome(t)
	home, err := shellDirectory("")
	if err != nil {
		t.Fatal(err)
	}
	s, ts := testServer(t)
	body, _ := json.Marshal(executeRequest{Command: os.Args[0], Args: []string{"-test.run=^TestExecuteCommandHelper$"}})
	status, data := requestAPI(t, s, ts, "POST", "/api/commands", string(body))
	if status != http.StatusOK {
		t.Fatalf("execute: HTTP %d %s", status, data)
	}
	var events []commandEvent
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var event commandEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		events = append(events, event)
	}
	joined := string(data)
	if !strings.Contains(joined, `"stream":"stdout"`) || !strings.Contains(joined, `"stream":"stderr"`) || !strings.Contains(joined, home) || events[len(events)-1].Type != "exit" || events[len(events)-1].Code != 0 {
		t.Fatalf("events = %s", data)
	}
	office := filepath.Join(root, "trusted")
	if err := os.MkdirAll(filepath.Join(office, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(office, ".omo", "omo.yaml"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project, err := TrustProject(office)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(executeRequest{Cwd: project.Path, Command: os.Args[0], Args: []string{"-test.run=^TestExecuteCommandHelper$"}})
	status, data = requestAPI(t, s, ts, "POST", "/api/commands", string(body))
	if status != http.StatusOK || !bytes.Contains(data, []byte(project.Path)) {
		t.Fatalf("trusted cwd: HTTP %d %s", status, data)
	}
	body, _ = json.Marshal(executeRequest{Cwd: t.TempDir(), Command: os.Args[0]})
	status, _ = requestAPI(t, s, ts, "POST", "/api/commands", string(body))
	if status != http.StatusBadRequest {
		t.Fatalf("untrusted cwd: HTTP %d", status)
	}
}

func TestExecuteCommandHelper(t *testing.T) {
	if os.Getenv("OMO_SUPERVISOR") != "1" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, cwd)
	fmt.Fprint(os.Stderr, "diagnostic")
	os.Exit(0)
}
