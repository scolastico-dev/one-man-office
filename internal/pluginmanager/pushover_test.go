package pluginmanager

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	bundledplugins "github.com/scolastico-dev/one-man-office/plugins"
)

const (
	testUserKey  = "uKEY-1234567890"
	testAppToken = "aTOKEN-abcdefghij"
)

// fakePushover is the Pushover API on 127.0.0.1: it records every form it
// gets and answers with status (200 unless set) and body.
type fakePushover struct {
	srv *httptest.Server

	mu     sync.Mutex
	forms  []url.Values
	status int
	body   string
	got    chan url.Values
}

func newFakePushover(t *testing.T) *fakePushover {
	t.Helper()
	f := &fakePushover{status: http.StatusOK, body: `{"status":1}`, got: make(chan url.Values, 64)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.ParseForm() != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.forms = append(f.forms, r.PostForm)
		status, body := f.status, f.body
		f.mu.Unlock()
		f.got <- r.PostForm
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePushover) answer(status int, body string) {
	f.mu.Lock()
	f.status, f.body = status, body
	f.mu.Unlock()
}

func (f *fakePushover) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.forms)
}

// next waits for the next request.
func (f *fakePushover) next(t *testing.T) url.Values {
	t.Helper()
	select {
	case v := <-f.got:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("no request reached the fake Pushover")
		return nil
	}
}

// pushoverRun is the bundled pushover plugin, installed and loaded in the
// plugin runtime, whose HTTP client is the fake's.
type pushoverRun struct {
	t    *testing.T
	m    *plugins.Manager
	fake *fakePushover
	db   *sql.DB
}

func newPushoverRun(t *testing.T, config map[string]any) *pushoverRun {
	t.Helper()
	fake := newFakePushover(t)
	e := newEnv(t)
	cfg := map[string]any{"user_key": testUserKey, "app_token": testAppToken, "api_url": fake.srv.URL + "/1/messages.json"}
	for k, v := range config {
		cfg[k] = v
	}
	s, _, err := e.install(api.PluginSettings{Name: "pushover", Source: "builtin:pushover", Enabled: true, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := plugins.New(plugins.Options{Root: e.root, DB: d, HTTP: fake.srv.Client(), Socket: func() string { return "" }})
	t.Cleanup(func() {
		m.Close()
		_ = d.Close()
	})
	if errs := m.Load([]api.PluginSettings{s}); errs["pushover"] != nil {
		t.Fatal(errs["pushover"])
	}
	return &pushoverRun{t: t, m: m, fake: fake, db: d}
}

func ticket(n int, title, attention string) map[string]any {
	return plugins.TicketPayload(api.TicketSummary{Number: n, Type: "task", Title: title, Column: "in_progress", Attention: attention},
		"http://127.0.0.1:8090/?token=board-secret")
}

func (r *pushoverRun) attention(n int, title, reason string) {
	r.m.Dispatch(plugins.Event{Name: plugins.EventNeedsAttention, At: time.Now(),
		Data: map[string]any{"ticket": ticket(n, title, reason), "reason": reason}})
}

func (r *pushoverRun) question(n int, title, body string, to api.Role) {
	c := api.Comment{ID: 5, Ticket: n, Author: api.Actor{Role: api.RoleDeveloper, Name: "dev-1"}, Body: body, QuestionTo: to}
	r.m.Dispatch(plugins.Event{Name: plugins.EventQuestionAsked, At: time.Now(),
		Data: map[string]any{"ticket": ticket(n, title, ""), "comment": plugins.CommentPayload(c)}})
}

// settle waits until no run of the plugin is going on and returns its log.
func (r *pushoverRun) settle() string {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		for _, i := range r.m.Infos() {
			if i.Name == "pushover" {
				state = i.State
			}
		}
		if state != plugins.StateRunning {
			var b strings.Builder
			for _, l := range r.m.LogLines("pushover", plugins.LogLines) {
				b.WriteString(l.Message + "\n")
			}
			return b.String()
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatal("the plugin did not settle")
	return ""
}

func (r *pushoverRun) state() api.PluginInfo {
	for _, i := range r.m.Infos() {
		if i.Name == "pushover" {
			return i
		}
	}
	r.t.Fatal("no pushover info")
	return api.PluginInfo{}
}

func TestPushoverSendsOnAttention(t *testing.T) {
	r := newPushoverRun(t, map[string]any{"priority": 1, "sound": "magic"})

	// A question to the pm sends nothing; the attention after it does.
	r.question(3, "Not for you", "pm only?", api.RolePM)
	r.attention(7, "Fix login", "The tests fail on main")
	form := r.fake.next(t)
	want := map[string]string{
		"token": testAppToken, "user": testUserKey, "title": "omo: a ticket needs your attention",
		"message": "#7 Fix login: The tests fail on main", "priority": "1", "sound": "magic",
		"url": "http://127.0.0.1:8090/7",
	}
	for k, v := range want {
		if form.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, form.Get(k), v)
		}
	}
	if len(form) != len(want) {
		t.Errorf("form = %v, want only %v", form, want)
	}
	r.settle()
	if n := r.fake.count(); n != 1 {
		t.Fatalf("%d requests, want 1 (the question to the pm sends nothing)", n)
	}

	// A question to the user sends its body.
	r.question(9, "Pick a name", "Which name do you want?", api.RoleUser)
	form = r.fake.next(t)
	if form.Get("title") != "omo: a question for you" || form.Get("message") != "#9 Pick a name: Which name do you want?" {
		t.Fatalf("question form = %v", form)
	}
	if log := r.settle(); log != "" {
		t.Fatalf("log after successful sends = %q", log)
	}
	if st := r.state(); st.State != plugins.StateReady {
		t.Fatalf("state = %s (%s)", st.State, st.Message)
	}

	// Other events of the board never reach a notification: the plugin has
	// no hook for them.
	r.m.Dispatch(plugins.Event{Name: plugins.EventCommentWritten, At: time.Now(), Data: map[string]any{"ticket": ticket(1, "x", "")}})
	r.attention(8, "Empty reason", "")
	r.attention(10, "Last", "done")
	if form := r.fake.next(t); form.Get("message") != "#10 Last: done" {
		t.Fatalf("message = %q", form.Get("message"))
	}
	r.settle()
	if n := r.fake.count(); n != 3 {
		t.Fatalf("%d requests, want 3", n)
	}
}

func TestPushoverKeepsHostileTextAsText(t *testing.T) {
	r := newPushoverRun(t, nil)
	hostile := "%s %d %n %q %% $(touch /tmp/x) `id` ${HOME} ‮​\x1b[31m" + `"]] .. os.exit() .. [[`
	long := strings.Repeat("é", 2000)
	r.attention(42, hostile, long)
	form := r.fake.next(t)
	full := "#42 " + hostile + ": " + long
	want := string([]rune(full)[:1024])
	if got := form.Get("message"); got != want {
		t.Fatalf("message = %q…, want the text cut to 1024 characters", got[:min(len(got), 80)])
	}
	if utf8.RuneCountInString(form.Get("message")) != 1024 || !utf8.ValidString(form.Get("message")) {
		t.Fatal("message is not 1024 whole characters")
	}
	if log := r.settle(); log != "" {
		t.Fatalf("log = %q", log)
	}
}

func TestPushoverFailuresAreLoggedNotThrown(t *testing.T) {
	r := newPushoverRun(t, nil)
	r.fake.answer(http.StatusBadRequest, `{"status":0,"errors":["application token `+testAppToken+` is invalid, user `+testUserKey+`"]}`)
	r.attention(1, "One", "needs you")
	r.fake.next(t)
	log := r.settle()
	if !strings.Contains(log, "status 400") {
		t.Fatalf("log = %q, want the status", log)
	}
	st := r.state()
	if st.State != plugins.StateReady || st.Message != "" {
		t.Fatalf("state = %s (%s), want ready: a failed send is not an error of the plugin", st.State, st.Message)
	}

	// The test action answers with the outcome.
	msg, err := r.m.Trigger(context.Background(), "pushover", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "Not sent: Pushover answered with status 400." {
		t.Fatalf("answer = %q", msg)
	}
	r.fake.next(t)
	r.fake.answer(http.StatusOK, `{"status":1}`)
	msg, err = r.m.Trigger(context.Background(), "pushover", "test", nil)
	if err != nil || msg != "Sent a test notification." {
		t.Fatalf("answer = %q, %v", msg, err)
	}
	if form := r.fake.next(t); form.Get("message") != "Test notification from omo." || form.Get("token") != testAppToken {
		t.Fatalf("test form = %v", form)
	}

	// No connection: logged, not thrown.
	r.fake.srv.Close()
	r.attention(2, "Two", "needs you")
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(r.settle(), "not sent: http request failed") {
		if time.Now().After(deadline) {
			t.Fatalf("log = %q", r.settle())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := r.state(); st.State != plugins.StateReady {
		t.Fatalf("state = %s (%s)", st.State, st.Message)
	}
	all := r.settle()
	for _, secret := range []string{testAppToken, testUserKey, "board-secret"} {
		if strings.Contains(all, secret) || strings.Contains(msg, secret) {
			t.Fatalf("log holds %q: %q", secret, all)
		}
	}
}

func TestPushoverWithoutCredentialsSendsNothing(t *testing.T) {
	r := newPushoverRun(t, map[string]any{"app_token": ""})
	r.attention(1, "One", "needs you")
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(r.settle(), "user_key and app_token are not set") {
		if time.Now().After(deadline) {
			t.Fatalf("log = %q", r.settle())
		}
		time.Sleep(10 * time.Millisecond)
	}
	msg, err := r.m.Trigger(context.Background(), "pushover", "test", nil)
	if err != nil || !strings.HasPrefix(msg, "Not sent: set user_key and app_token") {
		t.Fatalf("answer = %q, %v", msg, err)
	}
	if n := r.fake.count(); n != 0 {
		t.Fatalf("%d requests without credentials", n)
	}
	if strings.Contains(r.settle(), testUserKey) {
		t.Fatal("log holds the user key")
	}
}

func TestPushoverReachesOnlyAllowedAddresses(t *testing.T) {
	// http to a host that is not loopback is refused by the runtime's
	// helper before anything is sent.
	r := newPushoverRun(t, map[string]any{"api_url": "http://pushover.example/1/messages.json"})
	r.attention(1, "One", "needs you")
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(r.settle(), "not sent: http address not allowed") {
		if time.Now().After(deadline) {
			t.Fatalf("log = %q", r.settle())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := r.fake.count(); n != 0 {
		t.Fatalf("%d requests", n)
	}
}

func TestPushoverHoldsBackAfterItsHourlyCap(t *testing.T) {
	r := newPushoverRun(t, map[string]any{"max_per_hour": 3})
	for i := 1; i <= 6; i++ {
		r.attention(i, fmt.Sprintf("T%d", i), "needs you")
	}
	// The plugin counts what it held back: the fourth (whose place the
	// notice took), the fifth and the sixth.
	deadline := time.Now().Add(10 * time.Second)
	for r.window()["held"] != 3 {
		if time.Now().After(deadline) {
			t.Fatalf("window = %v, want 3 held back", r.window())
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.fake.mu.Lock()
	forms := append([]url.Values(nil), r.fake.forms...)
	r.fake.mu.Unlock()
	if len(forms) != 4 {
		t.Fatalf("%d requests, want 3 notifications and one notice", len(forms))
	}
	for i, f := range forms[:3] {
		if want := fmt.Sprintf("#%d T%d: needs you", i+1, i+1); f.Get("message") != want {
			t.Errorf("request %d = %q, want %q", i, f.Get("message"), want)
		}
	}
	if msg := forms[3].Get("message"); !strings.Contains(msg, "further notifications are held back until") || forms[3].Get("token") != testAppToken {
		t.Fatalf("notice = %v", forms[3])
	}
	for len(r.fake.got) > 0 {
		<-r.fake.got
	}

	// The test action is the user's own and is not counted or held back.
	if msg, err := r.m.Trigger(context.Background(), "pushover", "test", nil); err != nil || msg != "Sent a test notification." {
		t.Fatalf("test action: %q, %v", msg, err)
	}
	r.fake.next(t)

	// Once the hour is over, notifications go out again, and the log says
	// how many were held back.
	w := r.window()
	w["start"] -= 3601
	moved, _ := json.Marshal(w)
	if err := db.PluginStoreSet(r.db, "pushover", "rate_window", string(moved), plugins.MaxStoreKeys); err != nil {
		t.Fatal(err)
	}
	r.attention(7, "T7", "needs you")
	if form := r.fake.next(t); form.Get("message") != "#7 T7: needs you" {
		t.Fatalf("after the hour: %v", form)
	}
	if log := r.settle(); !strings.Contains(log, "3 notifications were held back") {
		t.Fatalf("log = %q", log)
	}
}

// window is the plugin's stored rate window.
func (r *pushoverRun) window() map[string]float64 {
	r.t.Helper()
	raw, ok, err := db.PluginStoreGet(r.db, "pushover", "rate_window")
	if err != nil {
		r.t.Fatal(err)
	}
	w := map[string]float64{}
	if ok {
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			r.t.Fatal(err)
		}
	}
	return w
}

func TestPushoverDefaultCap(t *testing.T) {
	raw, err := bundledplugins.ReadFile(bundledplugins.Pushover, plugins.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	m, err := plugins.ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(m.DefaultConfig["max_per_hour"]); got != "20" {
		t.Fatalf("default max_per_hour = %v, want 20", got)
	}
}
