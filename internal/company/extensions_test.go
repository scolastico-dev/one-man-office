package company

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestCompanyLoadsGlobalBrowserExtensionAndStartupHook(t *testing.T) {
	root := projectHome(t)
	plugin := filepath.Join(root, "global", "plugins", "dashboard")
	if err := os.MkdirAll(filepath.Join(plugin, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "global", "config.yaml"), []byte(`trusted_offices: []
plugins:
  update_on_start: false
  installed:
    dashboard:
      source: https://example.test/dashboard.git
      enabled: true
      config:
        nested:
          mode: careful
          list: [one, two]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"dashboard","hooks":[{"event":"company_startup","lua":"startup.lua"},{"event":"company_load","javascript":"web/main.js","files":["web/theme.css"]}]}`
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
	if status != http.StatusOK || !bytes.Contains(body, []byte(`"plugin":"dashboard"`)) || !bytes.Contains(body, []byte("web/main.js")) || bytes.Contains(body, []byte(`"files"`)) {
		t.Fatalf("extensions: HTTP %d %s", status, body)
	}
	var extensions []clientExtension
	if err := json.Unmarshal(body, &extensions); err != nil || len(extensions) < 2 {
		t.Fatalf("decode extensions: %+v %v", extensions, err)
	}
	dashboard := -1
	for i, extension := range extensions {
		if extension.Plugin == "dashboard" {
			dashboard = i
			break
		}
	}
	if dashboard < 0 {
		t.Fatalf("dashboard extension missing: %+v", extensions)
	}
	if got := extensions[dashboard].Config; got["nested"].(map[string]any)["mode"] != "careful" || len(got["nested"].(map[string]any)["list"].([]any)) != 2 {
		t.Fatalf("dashboard config = %#v", got)
	}
	if bytes.Contains(body, []byte(`"files"`)) || bytes.Contains(body, []byte(`"dir"`)) || bytes.Contains(body, []byte(`"configJSON"`)) {
		t.Fatalf("extensions exposed manager internals: %s", body)
	}
	resp, err := ts.Client().Do(authenticatedRequest(t, s, ts.URL+extensions[dashboard].Javascript))
	if err != nil {
		t.Fatal(err)
	}
	javascript, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(javascript, []byte("extensionLoaded")) {
		t.Fatalf("javascript: HTTP %d %s", resp.StatusCode, javascript)
	}
	resp, err = ts.Client().Get(ts.URL + extensions[dashboard].Javascript)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("script-tag request without bearer token: HTTP %d", resp.StatusCode)
	}
	if extensions[dashboard].Javascript != "/plugins/dashboard/web/main.js" {
		t.Fatalf("javascript URL = %q", extensions[dashboard].Javascript)
	}
	status, _ = requestAPI(t, s, ts, "GET", "/plugins/dashboard/private.txt", "")
	if status != http.StatusNotFound {
		t.Fatalf("undeclared file: HTTP %d", status)
	}
	var stored string
	if err := s.pluginDB.QueryRow(`SELECT value FROM plugin_storage WHERE scope='local' AND plugin='dashboard' AND key='started'`).Scan(&stored); err != nil || stored != "true" {
		t.Fatalf("startup state = %q, %v", stored, err)
	}
}

func TestPluginFileURLsAreNamespacedByManifestName(t *testing.T) {
	one := pluginFileURL("one", "web/main.js")
	two := pluginFileURL("two", "web/main.js")
	if one != "/plugins/one/web/main.js" || two != "/plugins/two/web/main.js" || one == two {
		t.Fatalf("plugin URLs = %q and %q", one, two)
	}
}

func TestCompanyHTTPOverlayPrecedesEmbeddedAndPluginFiles(t *testing.T) {
	root := projectHome(t)
	plugin := filepath.Join(root, "global", "plugins", "dashboard")
	if err := os.MkdirAll(filepath.Join(plugin, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "global", "config.yaml"), []byte(`trusted_offices: []
plugins:
  update_on_start: false
  installed:
    dashboard:
      source: https://example.test/dashboard.git
      enabled: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"name":"dashboard","hooks":[{"event":"company_load","javascript":"web/main.js"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "web", "main.js"), []byte("plugin"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{MaxAgents: 2})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })
	if err := os.MkdirAll(filepath.Join(s.httpRoot, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.httpRoot, "assets", "app.css"), []byte("overlay-css"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.httpRoot, "index.html"), []byte("overlay-index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.httpRoot, "plugins", "dashboard"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.httpRoot, "plugins", "dashboard", "web.js"), []byte("overlay-plugin"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, want, contentType string
	}{
		{path: "/", want: "overlay-index", contentType: "text/html; charset=utf-8"},
		{path: "/assets/app.css", want: "overlay-css", contentType: "text/css; charset=utf-8"},
		{path: "/plugins/dashboard/web.js", want: "overlay-plugin", contentType: "text/javascript; charset=utf-8"},
	} {
		resp, err := ts.Client().Get(ts.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != tc.want || resp.Header.Get("Content-Type") != tc.contentType {
			t.Fatalf("GET %s: HTTP %d, content type %q, body %q", tc.path, resp.StatusCode, resp.Header.Get("Content-Type"), body)
		}
		req, err := http.NewRequest(http.MethodHead, ts.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err = ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(body) != 0 || resp.Header.Get("Content-Type") != tc.contentType {
			t.Fatalf("HEAD %s: HTTP %d, content type %q, body %q", tc.path, resp.StatusCode, resp.Header.Get("Content-Type"), body)
		}
	}
	post, err := http.NewRequest(http.MethodPost, ts.URL+"/assets/app.css", strings.NewReader("ignored"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Do(post)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK && string(body) == "overlay-css" {
		t.Fatal("non-GET request was served by the overlay")
	}
}

func TestCompanyCreatesPrivateHTTPOverlayRoot(t *testing.T) {
	s, _ := testServer(t)
	info, err := os.Stat(s.httpRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("HTTP overlay root is not a directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("HTTP overlay root mode = %o, want 700", info.Mode().Perm())
	}
}

func TestCompanyHTTPOverlayRejectsUnsafeAndNonFiles(t *testing.T) {
	s, ts := testServer(t)
	root := s.httpRoot
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing.txt"), filepath.Join(root, "dangling.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	externalDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(externalDir, "external.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(externalDir, "external.txt"), filepath.Join(root, "external.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "missing.css"), filepath.Join(root, "assets", "app.css")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, path := range []string{"/directory/", "/dangling.txt", "/assets/app.css", "/nested/%2e%2e/nested/secret.txt", "/nested/../nested/secret.txt"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+s.authority+path, nil)
		recorder := httptest.NewRecorder()
		s.Handler().ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "secret") {
			t.Fatalf("unsafe overlay path %s served: HTTP %d body %q", path, recorder.Code, recorder.Body.String())
		}
	}
	resp, err := ts.Client().Get(ts.URL + "/external.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "secret" {
		t.Fatalf("external symlink: HTTP %d body %q", resp.StatusCode, body)
	}
}

func TestCompanyShutdownHookRunsBeforeOwnedInstancesStop(t *testing.T) {
	root := projectHome(t)
	plugin := filepath.Join(root, "global", "plugins", "shutdown")
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "global", "config.yaml"), []byte(`trusted_offices: []
plugins:
  update_on_start: false
  installed:
    shutdown:
      source: https://example.test/shutdown.git
      enabled: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"name":"shutdown","hooks":[{"event":"company_shutdown","lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "hook.lua"), []byte(`omo.local_set("home", event.data.home_path)`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{MaxAgents: 2})
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "global")
	s.Close()
	database, err := db.OpenReadOnly(filepath.Join(home, "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var got string
	if err := database.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='shutdown' AND key='home'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != `"`+home+`"` {
		t.Fatalf("company shutdown home_path = %q, want %q", got, home)
	}
}

func TestCompanyShutdownHookRunsBeforeOwnedInstanceKill(t *testing.T) {
	root := projectHome(t)
	plugin := filepath.Join(root, "global", "plugins", "boundary")
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "global", "config.yaml"), []byte(`trusted_offices: []
plugins:
  update_on_start: false
  installed:
    boundary:
      source: https://example.test/boundary.git
      enabled: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"name":"boundary","hooks":[{"event":"company_shutdown","lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "hook.lua"), []byte(`omo.local_set("entered", true); while not omo.local_get("release") do end`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{MaxAgents: 2})
	if err != nil {
		t.Fatal(err)
	}
	p := terminalFixture()
	instance := ownInstance("owned", root, "shell", p, nil)
	s.instances["owned"] = instance
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	entered := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var value string
		if err := s.pluginDB.QueryRow(`SELECT value FROM plugin_storage WHERE scope='local' AND plugin='boundary' AND key='entered'`).Scan(&value); err == nil && value == "true" {
			entered = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !entered {
		t.Fatal("company shutdown hook did not start")
	}
	select {
	case <-p.closed:
		t.Fatal("owned instance was killed before company shutdown hook completed")
	default:
	}
	if _, err := s.pluginDB.Exec(`INSERT INTO plugin_storage(scope, plugin, key, value) VALUES('local', 'boundary', 'release', 'true') ON CONFLICT(scope, plugin, key) DO UPDATE SET value=excluded.value, updated_at=datetime('now')`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.closed:
		close(p.exited)
	case <-time.After(5 * time.Second):
		t.Fatal("company did not kill owned instance after releasing shutdown hook")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("company close did not finish")
	}
}

func TestBrowserPluginAPIStaysDeliberatelySmall(t *testing.T) {
	_, server := testServer(t)
	resp, err := server.Client().Get(server.URL + "/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	script, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(script, []byte("Object.freeze({execute, $, ids, onLoad, token, dialog})")) {
		t.Fatalf("small browser API missing: %s", script)
	}
	for _, forbidden := range []string{"registerAction", "getState", "onState"} {
		if bytes.Contains(script, []byte(forbidden)) {
			t.Fatalf("browser API exposes %s", forbidden)
		}
	}
	for _, stableID := range []string{"supervisor-sidebar", "supervisor-main", "supervisor-toolbar"} {
		if !bytes.Contains(script, []byte(stableID)) {
			t.Fatalf("stable plugin DOM ID changed: %s", stableID)
		}
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
	if os.Getenv("OMO_COMPANY") != "1" {
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
