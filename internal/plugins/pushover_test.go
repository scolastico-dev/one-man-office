package plugins

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

const (
	pushoverToken = "app-token-must-never-leak"
	pushoverUser  = "user-key-must-never-leak"
)

type pushoverServer struct {
	mu       sync.Mutex
	requests []url.Values
	status   int
	body     string
}

func newPushoverServer(t *testing.T, status int, body string) (*httptest.Server, *pushoverServer) {
	t.Helper()
	capture := &pushoverServer{status: status, body: body}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read pushover request: %v", err)
			return
		}
		values, err := url.ParseQuery(string(raw))
		if err != nil {
			t.Errorf("parse pushover form: %v", err)
			return
		}
		capture.mu.Lock()
		capture.requests = append(capture.requests, values)
		capture.mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server, capture
}

func (s *pushoverServer) snapshot() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.requests...)
}

func loadPushover(t *testing.T, config map[string]any) (*Manager, *sql.DB) {
	t.Helper()
	office, database := newPluginOffice(t)
	source := pushoverSourceDir(t)
	target := filepath.Join(office, Dir, "pushover")
	for _, name := range []string{"plugin.json", "cron.lua", "notify.lua", "prompt.lua"} {
		raw, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := LoadConfigured(office, database, map[string]Settings{"pushover": {Enabled: true, Config: config}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, database
}

func pushoverSourceDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", "plugins", "pushover")
}

func emitPushoverCron(t *testing.T, manager *Manager, at, started int64, office string, inbox ...map[string]any) {
	t.Helper()
	entries := make([]any, len(inbox))
	for i, entry := range inbox {
		entries[i] = entry
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventCron, Data: map[string]any{
		"at_unix": at, "office_path": office, "office_started_at_unix": started, "user_inbox": entries,
	}}); err != nil {
		t.Fatal(err)
	}
}

func pushoverMail(id int64, from, subject string) map[string]any {
	return map[string]any{"id": id, "from": from, "subject": subject, "priority": "normal", "created_at_unix": id}
}

func allPushoverText(t *testing.T, database *sql.DB, extra ...string) string {
	t.Helper()
	var parts []string
	parts = append(parts, extra...)
	events, err := db.AllEvents(database)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		parts = append(parts, event.Detail)
	}
	logs, err := db.PluginLogs(database, "pushover")
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range logs {
		parts = append(parts, log.Message)
	}
	return strings.Join(parts, "\n")
}

func TestPushoverManifestDeclaresContract(t *testing.T) {
	manifest, err := ReadManifest(pushoverSourceDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "pushover" || manifest.Version == "" || manifest.Description == "" {
		t.Fatalf("manifest identity = %+v", manifest)
	}
	if len(manifest.Hooks) != 3 {
		t.Fatalf("hooks = %+v", manifest.Hooks)
	}
	if manifest.Hooks[0].Event != EventCron || manifest.Hooks[0].Interval != "1m" || manifest.Hooks[0].IntervalConfig != "check_interval" || manifest.Hooks[0].Lua != "cron.lua" {
		t.Fatalf("cron hook = %+v", manifest.Hooks[0])
	}
	if manifest.Hooks[1].Event != EventManual || manifest.Hooks[1].Name != "notify" || manifest.Hooks[1].ManualArgs != true || strings.Join(manifest.Hooks[1].Roles, ",") != "user,ceo" {
		t.Fatalf("manual hook = %+v", manifest.Hooks[1])
	}
	if manifest.Hooks[2].Event != EventPromptRender || manifest.Hooks[2].Lua != "prompt.lua" {
		t.Fatalf("prompt hook = %+v", manifest.Hooks[2])
	}
	want := map[string]any{"user_key": "", "app_token": "", "api_url": "https://api.pushover.net/1/messages.json", "check_interval": "1m", "stable_window": "5m", "priority": "0", "sound": ""}
	for key, value := range want {
		if got := fmt.Sprint(manifest.DefaultConfig[key]); got != value {
			t.Fatalf("default_config[%q] = %#v, want %#v", key, got, value)
		}
	}
}

func TestPushoverMissingCredentialsLogOncePerStartupAndRedactSecrets(t *testing.T) {
	manager, database := loadPushover(t, map[string]any{"user_key": "", "app_token": "", "office_secret": pushoverToken + pushoverUser})
	emitPushoverCron(t, manager, 100, 42, "/office", pushoverMail(1, "alice", "secret subject"))
	emitPushoverCron(t, manager, 101, 42, "/office", pushoverMail(1, "alice", "secret subject"))
	emitPushoverCron(t, manager, 102, 43, "/office", pushoverMail(1, "alice", "secret subject"))
	logs, err := db.PluginLogs(database, "pushover")
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("missing-credential logs = %+v, want one per startup", logs)
	}
	err = manager.TriggerManualContextWithRole(context.Background(), "pushover", "notify", "user", "user", []string{"hello"})
	if err == nil || !strings.Contains(err.Error(), "pushover credentials are not configured") {
		t.Fatalf("missing-credential manual error = %v", err)
	}
	text := allPushoverText(t, database, err.Error())
	if strings.Contains(text, pushoverToken) || strings.Contains(text, pushoverUser) {
		t.Fatalf("pushover secret leaked in durable output: %s", text)
	}
}

func TestPushoverInboxCycleStabilityResetAndNoResend(t *testing.T) {
	server, capture := newPushoverServer(t, http.StatusOK, `{"status":1}`)
	manager, database := loadPushover(t, map[string]any{
		"user_key": pushoverUser, "app_token": pushoverToken, "api_url": server.URL, "stable_window": "5m", "priority": 0,
	})
	mail := pushoverMail(1, "alice", "first")
	emitPushoverCron(t, manager, 100, 7, "/office", mail)
	emitPushoverCron(t, manager, 399, 7, "/office", mail)
	if got := len(capture.snapshot()); got != 0 {
		t.Fatalf("sent before stability window: %d", got)
	}
	emitPushoverCron(t, manager, 400, 7, "/office", pushoverMail(2, "bob", "changed"))
	emitPushoverCron(t, manager, 699, 7, "/office", pushoverMail(2, "bob", "changed"))
	if got := len(capture.snapshot()); got != 0 {
		t.Fatalf("sent after changed set before full window: %d", got)
	}
	emitPushoverCron(t, manager, 700, 7, "/office", pushoverMail(2, "bob", "changed"))
	emitPushoverCron(t, manager, 701, 7, "/office", pushoverMail(2, "bob", "changed"), pushoverMail(3, "carol", "added"))
	emitPushoverCron(t, manager, 1000, 7, "/office", pushoverMail(2, "bob", "changed"), pushoverMail(3, "carol", "added"))
	if got := len(capture.snapshot()); got != 1 {
		t.Fatalf("nonempty cycle sends = %d, want one", got)
	}
	emitPushoverCron(t, manager, 1001, 7, "/office")
	emitPushoverCron(t, manager, 1002, 7, "/office", pushoverMail(9, "dana", "next"))
	emitPushoverCron(t, manager, 1302, 7, "/office", pushoverMail(9, "dana", "next"))
	if got := len(capture.snapshot()); got != 2 {
		t.Fatalf("reset cycle sends = %d, want two total", got)
	}
	if text := allPushoverText(t, database); strings.Contains(text, pushoverToken) || strings.Contains(text, pushoverUser) {
		t.Fatalf("credentials leaked in cycle output: %s", text)
	}
}

func TestPushoverBodyFormAndUTF8Limits(t *testing.T) {
	server, capture := newPushoverServer(t, http.StatusOK, `{}`)
	manager, _ := loadPushover(t, map[string]any{
		"user_key": pushoverUser, "app_token": pushoverToken, "api_url": server.URL, "stable_window": "1s", "priority": 2, "sound": "siren",
	})
	long := strings.Repeat("界", 900)
	mail := make([]map[string]any, 7)
	for i := range mail {
		subject := "subject-" + fmt.Sprint(i+1)
		if i == 4 {
			subject += long
		}
		mail[i] = pushoverMail(int64(i+1), "sender-"+fmt.Sprint(i+1), subject)
	}
	emitPushoverCron(t, manager, 100, 7, "/very/important/office", mail...)
	emitPushoverCron(t, manager, 101, 7, "/very/important/office", mail...)
	requests := capture.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want one", len(requests))
	}
	form := requests[0]
	if form.Get("token") != pushoverToken || form.Get("user") != pushoverUser || form.Get("priority") != "2" || form.Get("sound") != "siren" {
		t.Fatalf("form credentials/options = %#v", form)
	}
	body := form.Get("message")
	if !utf8.ValidString(body) || utf8.RuneCountInString(body) != 1024 {
		t.Fatalf("message UTF-8 length = valid:%v runes:%d", utf8.ValidString(body), utf8.RuneCountInString(body))
	}
	if !strings.HasPrefix(body, "7 unread messages for you in office /very/important/office") || strings.Count(body, "from ") != 5 || strings.Contains(body, "sender-6-") || strings.Contains(body, "sender-7-") {
		t.Fatalf("message body prefix=%v lines=%d has6=%v has7=%v", strings.HasPrefix(body, "7 unread messages for you in office /very/important/office"), strings.Count(body, "from "), strings.Contains(body, "sender-6-"), strings.Contains(body, "sender-7-"))
	}
}

func TestPushoverManualTitlesValidationAndFailureRedaction(t *testing.T) {
	server, capture := newPushoverServer(t, http.StatusOK, `{}`)
	manager, _ := loadPushover(t, map[string]any{"user_key": pushoverUser, "app_token": pushoverToken, "api_url": server.URL})
	if err := manager.TriggerManualContextWithRole(context.Background(), "pushover", "notify", "user", "user", []string{"hello"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.TriggerManualContextWithRole(context.Background(), "pushover", "notify", "ceo-ada", "ceo", []string{"decision", "Phone title"}); err != nil {
		t.Fatal(err)
	}
	requests := capture.snapshot()
	if len(requests) != 2 || requests[0].Get("title") != "omo user" || requests[1].Get("title") != "omo CEO: Phone title" {
		t.Fatalf("manual titles = %#v", requests)
	}
	if err := manager.TriggerManualContextWithRole(context.Background(), "pushover", "notify", "ceo-ada", "ceo", []string{"long", strings.Repeat("界", 300)}); err != nil {
		t.Fatal(err)
	}
	requests = capture.snapshot()
	if got := requests[2].Get("title"); !utf8.ValidString(got) || utf8.RuneCountInString(got) != 250 || !strings.HasPrefix(got, "omo CEO: ") {
		t.Fatalf("truncated CEO title = valid:%v runes:%d value=%q", utf8.ValidString(got), utf8.RuneCountInString(got), got)
	}
	for _, request := range requests {
		if strings.Contains(request.Get("title"), pushoverToken) || strings.Contains(request.Get("title"), pushoverUser) {
			t.Fatalf("credential leaked into title: %q", request.Get("title"))
		}
	}
	for _, args := range [][]string{nil, {""}, {"one", "two", "three"}} {
		if err := manager.TriggerManualContextWithRole(context.Background(), "pushover", "notify", "user", "user", args); err == nil {
			t.Fatalf("accepted invalid args %#v", args)
		}
	}
}

func TestPushoverNon2xxAndManualFailureAreSanitized(t *testing.T) {
	server, capture := newPushoverServer(t, http.StatusBadRequest, `{"errors":["invalid token"]}`)
	manager, database := loadPushover(t, map[string]any{"user_key": pushoverUser, "app_token": pushoverToken, "api_url": server.URL, "stable_window": "1s"})
	emitPushoverCron(t, manager, 100, 7, "/office", pushoverMail(1, "alice", "subject"))
	emitPushoverCron(t, manager, 101, 7, "/office", pushoverMail(1, "alice", "subject"))
	if len(capture.snapshot()) != 1 {
		t.Fatalf("failed cron requests = %d, want one", len(capture.snapshot()))
	}
	err := manager.TriggerManualContextWithRole(context.Background(), "pushover", "notify", "user", "user", []string{"hello"})
	if err == nil || !strings.Contains(err.Error(), "pushover notification failed") {
		t.Fatalf("manual failure = %v", err)
	}
	text := allPushoverText(t, database, err.Error())
	if !strings.Contains(text, "status 400") || !strings.Contains(text, "invalid token") {
		t.Fatalf("non-2xx log = %s", text)
	}
	if strings.Contains(text, pushoverToken) || strings.Contains(text, pushoverUser) {
		t.Fatalf("secret leaked in non-2xx output: %s", text)
	}
}

func TestPushoverCEOOnlyPromptNote(t *testing.T) {
	manager, _ := loadPushover(t, map[string]any{})
	note := "The pushover plugin is installed. To reach the user on their phone run `omo plugin trigger pushover notify -- \"<message>\"`. Use it for blockers and decisions that need the user; routine reports stay in mail."
	ceo, err := manager.RenderPrompt(context.Background(), "ceo", "ceo-ada", 1, "base")
	if err != nil || strings.Count(ceo, note) != 1 {
		t.Fatalf("CEO prompt = %q, err=%v", ceo, err)
	}
	developer, err := manager.RenderPrompt(context.Background(), "developer", "developer-ada", 1, "base")
	if err != nil || developer != "base" {
		t.Fatalf("developer prompt = %q, err=%v", developer, err)
	}
}
