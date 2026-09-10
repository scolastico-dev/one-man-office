package plugins

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	officedb "github.com/scolastico-dev/one-man-office/internal/db"
)

func TestLuaHTTPPostFormAndResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("X-Test-Header"); got != "header-value" {
			t.Errorf("X-Test-Header = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		if got := string(body); got != "message=hello+world&token=secret" {
			t.Errorf("request body = %q", got)
		}
		w.Header().Add("X-Response", "one")
		w.Header().Add("X-Response", "two")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("response body"))
	}))
	defer server.Close()

	manager, _, _ := newLuaHTTPManager(t, fmt.Sprintf(`
local response, err = omo.http{
  method = "POST",
  url = %q,
  headers = { ["X-Test-Header"] = "header-value" },
  form = { token = "secret", message = "hello world" },
}
assert(err == nil, err)
assert(response.status == 202)
assert(response.body == "response body")
assert(response.headers["X-Response"][1] == "one")
assert(response.headers["X-Response"][2] == "two")
`, server.URL))
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart}); err != nil {
		t.Fatalf("HTTP hook: %v", err)
	}
}

func TestLuaHTTPJSONRawBodyEmptyAndDefaultGET(t *testing.T) {
	tests := []struct {
		name       string
		scriptBody string
		wantMethod string
		wantType   string
		wantBody   string
	}{
		{name: "json", scriptBody: `json = { answer = 42, ok = true }`, wantMethod: http.MethodPost, wantType: "application/json", wantBody: `{"answer":42,"ok":true}`},
		{name: "raw body", scriptBody: `body = "raw payload"`, wantMethod: http.MethodPut, wantBody: "raw payload"},
		{name: "empty get", scriptBody: "", wantMethod: http.MethodGet, wantBody: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.wantMethod {
					t.Errorf("method = %s, want %s", r.Method, test.wantMethod)
				}
				if test.wantType != "" && r.Header.Get("Content-Type") != test.wantType {
					t.Errorf("Content-Type = %q, want %q", r.Header.Get("Content-Type"), test.wantType)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request: %v", err)
				}
				if string(body) != test.wantBody {
					t.Errorf("body = %q, want %q", body, test.wantBody)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer server.Close()

			request := fmt.Sprintf("url = %q", server.URL)
			if test.wantMethod != http.MethodGet {
				request += fmt.Sprintf(", method = %q", test.wantMethod)
			}
			request += ", " + test.scriptBody
			manager, _, _ := newLuaHTTPManager(t, fmt.Sprintf(`
local response, err = omo.http({%s})
assert(err == nil, err)
assert(response.status == 200)
assert(response.body == "ok")
`, request))
			if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart}); err != nil {
				t.Fatalf("HTTP hook: %v", err)
			}
		})
	}
}

func TestLuaHTTPCustomTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer server.Close()
	manager, _, _ := newLuaHTTPManager(t, fmt.Sprintf(`
local response, err = omo.http{url = %q, timeout = "20ms"}
assert(response == nil)
assert(err ~= nil)
`, server.URL))
	started := time.Now()
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart}); err != nil {
		t.Fatalf("HTTP timeout hook: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
		t.Fatalf("request exceeded custom timeout: %s", elapsed)
	}
}

func TestLuaHTTPSameHostRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			return
		}
		if r.Header.Get("X-Redirect-Header") != "redirect-value" {
			t.Errorf("redirect header = %q", r.Header.Get("X-Redirect-Header"))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "redirect-body" {
			t.Errorf("redirect body = %q", body)
		}
		_, _ = w.Write([]byte("redirected"))
	}))
	defer server.Close()
	manager, _, _ := newLuaHTTPManager(t, fmt.Sprintf(`
local response, err = omo.http{
  method = "POST",
  url = %q,
  headers = { ["X-Redirect-Header"] = "redirect-value" },
  body = "redirect-body",
}
assert(err == nil, err)
assert(response.status == 200)
assert(response.body == "redirected")
`, server.URL+"/redirect"))
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart}); err != nil {
		t.Fatalf("same-host redirect hook: %v", err)
	}
}

func TestLuaHTTPCrossHostRedirectDoesNotSendCredentials(t *testing.T) {
	var destinationHits int
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationHits++
		_, _ = w.Write([]byte("unexpected"))
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer origin.Close()
	manager, database, plugin := newLuaHTTPManager(t, fmt.Sprintf(`
local response, err = omo.http{
  url = %q,
  headers = { ["X-Cross-Host-Secret-Header"] = "cross-host-secret-value" },
  body = "cross-host-secret-body",
}
assert(response == nil)
assert(err ~= nil)
error(err)
`, origin.URL))
	_, err := manager.Emit(context.Background(), Event{Name: EventAgentStart})
	if err == nil {
		t.Fatal("cross-host redirect unexpectedly succeeded")
	}
	if destinationHits != 0 {
		t.Fatalf("cross-host destination received %d requests", destinationHits)
	}
	assertNoPluginSecret(t, database, plugin, err.Error(), "cross-host-secret-body", "X-Cross-Host-Secret-Header", "cross-host-secret-value")
}

func TestLuaHTTPResponseOverflowFailsWithoutLeakingSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 1<<20+1)))
	}))
	defer server.Close()
	manager, database, plugin := newLuaHTTPManager(t, fmt.Sprintf(`
local response, err = omo.http{
  url = %q,
  headers = { ["X-Overflow-Secret-Header"] = "overflow-secret-value" },
  form = { secret = "overflow-secret-form" },
}
assert(response == nil)
assert(err ~= nil)
error(err)
`, server.URL))
	_, err := manager.Emit(context.Background(), Event{Name: EventAgentStart})
	if err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("overflow error = %v", err)
	}
	assertNoPluginSecret(t, database, plugin, err.Error(), "overflow-secret-form", "X-Overflow-Secret-Header", "overflow-secret-value")
}

func TestLuaHTTPNetworkFailureDoesNotLeakSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	serverURL := server.URL
	server.Close()
	manager, database, plugin := newLuaHTTPManager(t, fmt.Sprintf(`
local response, err = omo.http{
  url = %q,
  headers = { ["X-Network-Secret-Header"] = "network-secret-value" },
  form = { token = "network-secret-form" },
}
assert(response == nil)
assert(err ~= nil)
error(err)
`, serverURL))
	_, err := manager.Emit(context.Background(), Event{Name: EventAgentStart})
	if err == nil {
		t.Fatal("network failure unexpectedly succeeded")
	}
	assertNoPluginSecret(t, database, plugin, err.Error(), "network-secret-form", "X-Network-Secret-Header", "network-secret-value")
}

func TestLuaHTTPRejectsMalformedTableInvalidDurationAndMultipleBodies(t *testing.T) {
	tests := []struct {
		name    string
		request string
	}{
		{name: "malformed header table", request: `url = "http://127.0.0.1", headers = { [1] = "value" }`},
		{name: "malformed form table", request: `url = "http://127.0.0.1", form = { token = 42 }`},
		{name: "invalid duration", request: `url = "http://127.0.0.1", timeout = "not-a-duration"`},
		{name: "multiple bodies", request: `url = "http://127.0.0.1", form = { token = "x" }, json = { value = true }`},
		{name: "unknown field", request: `url = "http://127.0.0.1", unexpected = "value"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, _, _ := newLuaHTTPManager(t, fmt.Sprintf(`
local response, err = omo.http{%s}
assert(response == nil)
assert(err ~= nil)
`, test.request))
			if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart}); err != nil {
				t.Fatalf("validation hook: %v", err)
			}
		})
	}
}

func TestLuaHTTPRejectsNonHTTPURL(t *testing.T) {
	manager, _, _ := newLuaHTTPManager(t, `
local response, err = omo.http{url = "ftp://example.test/file"}
assert(response == nil)
assert(err ~= nil)
`)
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart}); err != nil {
		t.Fatalf("URL validation hook: %v", err)
	}
}

func newLuaHTTPManager(t *testing.T, script string) (*Manager, *sql.DB, string) {
	t.Helper()
	office, database := newPluginOffice(t)
	plugin := "lua-http"
	writePlugin(t, filepath.Join(office, Dir, plugin), Manifest{Name: plugin, Hooks: []Hook{{Event: EventAgentStart, Lua: "hook.lua"}}}, script)
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, database, plugin
}

func assertNoPluginSecret(t *testing.T, database *sql.DB, plugin, returnedError string, secrets ...string) {
	t.Helper()
	check := func(where, value string) {
		for _, secret := range secrets {
			if strings.Contains(value, secret) {
				t.Fatalf("secret %q leaked in %s: %q", secret, where, value)
			}
		}
	}
	check("returned error", returnedError)
	rows, err := database.Query(`SELECT kind, detail FROM events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, detail string
		if err := rows.Scan(&kind, &detail); err != nil {
			t.Fatal(err)
		}
		check("event "+kind, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	logs, err := officedb.PluginLogs(database, plugin)
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range logs {
		check("plugin log", log.Message)
	}
}
