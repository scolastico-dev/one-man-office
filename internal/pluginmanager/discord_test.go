package pluginmanager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

const (
	testWebhookID    = "112233445566778899"
	testWebhookToken = "tOkEn_secret-ABCdef123"
	testWebhook      = "https://discord.com/api/webhooks/" + testWebhookID + "/" + testWebhookToken
	testRole         = "998877665544332211"
)

// discordAnswer is one answer of the fake Discord.
type discordAnswer struct {
	status  int
	headers map[string]string
	body    string
}

// fakeDiscord stands in for Discord's webhook API. The plugin is configured
// with real https://discord.com addresses; the runtime's HTTP client (a test
// seam of plugins.Options that no settings file reaches) takes every
// request for discord.com or discordapp.com to this local server and
// refuses every other host, recording it.
type fakeDiscord struct {
	srv *httptest.Server

	mu      sync.Mutex
	paths   []string
	bodies  []map[string]any
	answers []discordAnswer
	other   []string
	got     chan map[string]any
}

func newFakeDiscord(t *testing.T) *fakeDiscord {
	t.Helper()
	f := &fakeDiscord{got: make(chan map[string]any, 64)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || json.Unmarshal(raw, &body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.Header.Get("X-Original-Host")+r.URL.Path)
		f.bodies = append(f.bodies, body)
		a := discordAnswer{status: http.StatusNoContent}
		if len(f.answers) > 0 {
			a, f.answers = f.answers[0], f.answers[1:]
		}
		f.mu.Unlock()
		for k, v := range a.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
		f.got <- body
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// client is the HTTP client of the runtime under test.
func (f *fakeDiscord) client() *http.Client {
	target, _ := url.Parse(f.srv.URL)
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		host := r.URL.Hostname()
		if r.URL.Scheme != "https" || (host != "discord.com" && host != "discordapp.com") {
			f.mu.Lock()
			f.other = append(f.other, r.URL.String())
			f.mu.Unlock()
			return nil, errors.New("not Discord")
		}
		r = r.Clone(r.Context())
		r.Header.Set("X-Original-Host", host)
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// answer queues answers for the next requests (after them: 204).
func (f *fakeDiscord) answer(a ...discordAnswer) {
	f.mu.Lock()
	f.answers = append(f.answers, a...)
	f.mu.Unlock()
}

func (f *fakeDiscord) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func (f *fakeDiscord) next(t *testing.T) map[string]any {
	t.Helper()
	return <-f.got
}

type discordRun struct {
	t          *testing.T
	m          *plugins.Manager
	fake       *fakeDiscord
	db         *sql.DB
	dispatched int64
	completed  *atomic.Int64
}

// newDiscordRun installs the shipped discord plugin from a repository laid
// out as the release branch and loads it with config over its defaults.
func newDiscordRun(t *testing.T, config map[string]any) *discordRun {
	t.Helper()
	fake := newFakeDiscord(t)
	e := newEnv(t)
	cfg := map[string]any{"webhook_url": testWebhook}
	for k, v := range config {
		cfg[k] = v
	}
	want := shipped(shippedRepo(t), "discord", "discord")
	want.Config = cfg
	s, _, err := e.install(want)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	completed := &atomic.Int64{}
	m := plugins.New(plugins.Options{Root: e.root, DB: d, HTTP: fake.client(), Socket: func() string { return "" }, Changed: func(info api.PluginInfo) {
		if info.LastRunAt != nil && info.State != plugins.StateRunning && info.LastEvent != plugins.EventManual {
			completed.Add(1)
		}
	}})
	t.Cleanup(func() {
		m.Close()
		_ = d.Close()
	})
	if errs := m.Load([]api.PluginSettings{s}); errs["discord"] != nil {
		t.Fatal(errs["discord"])
	}
	return &discordRun{t: t, m: m, fake: fake, db: d, completed: completed}
}

func (r *discordRun) attention(n int, title, reason string) {
	r.dispatched++
	r.m.Dispatch(plugins.Event{Name: plugins.EventNeedsAttention, At: time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC),
		Data: map[string]any{"ticket": ticket(n, title, reason), "reason": reason}})
}

func (r *discordRun) question(n int, title, body string, to api.Role) {
	r.dispatched++
	c := api.Comment{ID: 5, Ticket: n, Author: api.Actor{Role: api.RoleDeveloper, Name: "dev-1"}, Body: body, QuestionTo: to}
	r.m.Dispatch(plugins.Event{Name: plugins.EventQuestionAsked, At: time.Now(),
		Data: map[string]any{"ticket": ticket(n, title, ""), "comment": plugins.CommentPayload(c)}})
}

func (r *discordRun) settle() string {
	r.t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for r.completed.Load() < r.dispatched {
		select {
		case <-tick.C:
		}
	}
	var b strings.Builder
	for _, l := range r.m.LogLines("discord", plugins.LogLines) {
		b.WriteString(l.Message + "\n")
	}
	return b.String()
}

func (r *discordRun) waitLog(text string) string {
	r.t.Helper()
	for {
		var b strings.Builder
		for _, l := range r.m.LogLines("discord", plugins.LogLines) {
			b.WriteString(l.Message + "\n")
		}
		if strings.Contains(b.String(), text) {
			return b.String()
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// waitHeld waits until the hourly window counts held messages.
func (r *discordRun) waitHeld(held int) {
	r.t.Helper()
	for {
		raw, ok, err := db.PluginStoreGet(r.db, "discord", "rate_window")
		var w struct {
			Held int `json:"held"`
		}
		if err == nil && ok && json.Unmarshal([]byte(raw), &w) == nil && w.Held == held {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func (r *discordRun) state() api.PluginInfo {
	for _, i := range r.m.Infos() {
		if i.Name == "discord" {
			return i
		}
	}
	r.t.Fatal("no discord info")
	return api.PluginInfo{}
}

func embedOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	list, ok := body["embeds"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("embeds = %#v", body["embeds"])
	}
	return list[0].(map[string]any)
}

func jsonText(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func TestDiscordSendsOnAttentionAndQuestion(t *testing.T) {
	r := newDiscordRun(t, nil)

	r.question(3, "Not for you", "pm only?", api.RolePM)
	r.attention(7, "Fix login", "The tests fail on main")
	body := r.fake.next(t)
	if got := r.fake.paths[0]; got != "discord.com/api/v10/webhooks/"+testWebhookID+"/"+testWebhookToken {
		t.Fatalf("posted to %s", got)
	}
	if body["content"] != "A ticket needs your attention" || body["username"] != "omo" {
		t.Fatalf("body = %s", jsonText(body))
	}
	if got := jsonText(body["allowed_mentions"]); got != `{"parse":[]}` {
		t.Fatalf("allowed_mentions = %s", got)
	}
	e := embedOf(t, body)
	if e["title"] != `\#7 Fix login` || e["description"] != "The tests fail on main" || e["url"] != "http://127.0.0.1:8090/7" ||
		e["timestamp"] != "2026-11-16T09:30:00Z" || e["color"] != float64(15105570) {
		t.Fatalf("embed = %s", jsonText(e))
	}

	r.question(9, "Pick a name", "Which name do you want?", api.RoleUser)
	body = r.fake.next(t)
	if body["content"] != "A question for you" || embedOf(t, body)["description"] != "Which name do you want?" {
		t.Fatalf("question body = %s", jsonText(body))
	}
	if log := r.settle(); log != "" {
		t.Fatalf("log after successful sends = %q", log)
	}
	if n := r.fake.count(); n != 2 {
		t.Fatalf("%d requests, want 2 (the question to the pm sends nothing)", n)
	}
	if st := r.state(); st.State != plugins.StateReady {
		t.Fatalf("state = %s (%s)", st.State, st.Message)
	}
}

// TestDiscordTicketTextCannotPing: markdown, mentions and links in a ticket
// are escaped, allowed_mentions never lets them ping, and only the
// configured role is allowed.
func TestDiscordTicketTextCannotPing(t *testing.T) {
	r := newDiscordRun(t, map[string]any{"mention": "role:" + testRole})
	hostile := "@everyone @here <@&123456789012345678> <@123456789012345678> **bold** [x](https://evil.example) `code` # head\n> quote\n- item ||spoiler|| :smile:"
	r.attention(42, hostile, hostile)
	body := r.fake.next(t)
	if body["content"] != "<@&"+testRole+"> A ticket needs your attention" {
		t.Fatalf("content = %q", body["content"])
	}
	if got := jsonText(body["allowed_mentions"]); got != `{"parse":[],"roles":["`+testRole+`"]}` {
		t.Fatalf("allowed_mentions = %s", got)
	}
	e := embedOf(t, body)
	want := `\@everyone \@here \<\@&123456789012345678\> \<\@123456789012345678\> \*\*bold\*\* \[x\]\(https\://evil\.example\) \` + "`" + `code\` + "`" + ` \# head` + "\n" + `\> quote` + "\n" + `\- item \|\|spoiler\|\| \:smile\:`
	if e["description"] != want {
		t.Fatalf("description = %q\nwant          %q", e["description"], want)
	}
	if title, _ := e["title"].(string); !strings.HasPrefix(title, `\#42 \@everyone`) || strings.Contains(title, " @") {
		t.Fatalf("title = %q", title)
	}
	r.settle()

	// A user mention allows that user only; a malformed one pings nobody.
	r2 := newDiscordRun(t, map[string]any{"mention": "user:" + testRole})
	r2.attention(1, "t", "r")
	if got := jsonText(r2.fake.next(t)["allowed_mentions"]); got != `{"parse":[],"users":["`+testRole+`"]}` {
		t.Fatalf("user allowed_mentions = %s", got)
	}
	r3 := newDiscordRun(t, map[string]any{"mention": "@everyone"})
	r3.attention(1, "t", "r")
	b3 := r3.fake.next(t)
	if b3["content"] != "A ticket needs your attention" || jsonText(b3["allowed_mentions"]) != `{"parse":[]}` {
		t.Fatalf("bad mention body = %s", jsonText(b3))
	}
	r3.waitLog("mention must be")
}

func TestDiscordRespectsLimits(t *testing.T) {
	r := newDiscordRun(t, map[string]any{"username": strings.Repeat("n", 100)})
	long := strings.Repeat("é", 5000)
	r.attention(5, strings.Repeat("t.", 300), long)
	body := r.fake.next(t)
	e := embedOf(t, body)
	desc, title := e["description"].(string), e["title"].(string)
	if n := utf8.RuneCountInString(desc); n != MaxDiscordDescription || !strings.HasSuffix(desc, "…") || !strings.HasPrefix(desc, "éé") {
		t.Fatalf("description has %d characters", n)
	}
	if n := utf8.RuneCountInString(title); n > 256 || !strings.HasSuffix(title, "…") || strings.HasSuffix(title, `\…`) {
		t.Fatalf("title %q has %d characters", title, n)
	}
	if name := body["username"].(string); utf8.RuneCountInString(name) != 80 || !strings.HasSuffix(name, "…") {
		t.Fatalf("username = %q", name)
	}
	r.settle()

	// A name Discord refuses is left out.
	r2 := newDiscordRun(t, map[string]any{"username": "My Discord bot"})
	r2.attention(1, "t", "r")
	if b := r2.fake.next(t); b["username"] != nil {
		t.Fatalf("username = %v", b["username"])
	}
}

// MaxDiscordDescription is Discord's limit of an embed description.
const MaxDiscordDescription = 4096

func TestDiscordRefusesOtherAddresses(t *testing.T) {
	for _, u := range []string{
		"http://discord.com/api/webhooks/" + testWebhookID + "/" + testWebhookToken,
		"https://evil.example/api/webhooks/" + testWebhookID + "/" + testWebhookToken,
		"https://discord.com.evil.example/api/webhooks/" + testWebhookID + "/" + testWebhookToken,
		"https://discord.com/api/webhooks/" + testWebhookID + "/" + testWebhookToken + "/../../x",
		"https://discord.com/api/webhooks/abc/" + testWebhookToken,
		"https://discord.com/api/webhooks/" + testWebhookID + "/" + testWebhookToken + "?wait=true",
		"https://user@discord.com/api/webhooks/" + testWebhookID + "/" + testWebhookToken,
		"https://discord.com:8443/api/webhooks/" + testWebhookID + "/" + testWebhookToken,
		"https://127.0.0.1/api/webhooks/" + testWebhookID + "/" + testWebhookToken,
	} {
		t.Run(u, func(t *testing.T) {
			r := newDiscordRun(t, map[string]any{"webhook_url": u})
			r.attention(1, "t", "r")
			r.waitLog("not a Discord webhook address")
			msg, err := r.m.Trigger(context.Background(), "discord", "test", nil)
			if err != nil || !strings.HasPrefix(msg, "Not sent: webhook_url must be a Discord webhook address") {
				t.Fatalf("test action: %q, %v", msg, err)
			}
			if r.fake.count() != 0 || len(r.fake.other) != 0 {
				t.Fatalf("requests: %d to Discord, others %v", r.fake.count(), r.fake.other)
			}
		})
	}
	// The other accepted forms.
	for _, u := range []string{
		"https://discordapp.com/api/webhooks/" + testWebhookID + "/" + testWebhookToken,
		"https://discord.com/api/v9/webhooks/" + testWebhookID + "/" + testWebhookToken,
	} {
		r := newDiscordRun(t, map[string]any{"webhook_url": u})
		r.attention(1, "t", "r")
		r.fake.next(t)
		host := strings.TrimPrefix(strings.SplitN(u, "/api/", 2)[0], "https://")
		if got := r.fake.paths[0]; got != host+"/api/v10/webhooks/"+testWebhookID+"/"+testWebhookToken {
			t.Fatalf("%s: posted to %s", u, got)
		}
	}
}

func TestDiscordRetries(t *testing.T) {
	r := newDiscordRun(t, nil)

	// 429: waits Retry-After, then sends.
	r.fake.answer(discordAnswer{status: 429, headers: map[string]string{"Retry-After": "0.2"}, body: `{"retry_after": 0.2, "global": false}`})
	start := time.Now()
	r.attention(1, "a", "b")
	r.fake.next(t)
	r.fake.next(t)
	if took := time.Since(start); took < 200*time.Millisecond {
		t.Fatalf("retried after %v, before Retry-After", took)
	}
	if log := r.settle(); !strings.Contains(log, "status 429") || !strings.Contains(log, "trying again in 0.2 s") {
		t.Fatalf("log = %q", log)
	}

	// retry_after of the body when the header is missing.
	r.fake.answer(discordAnswer{status: 429, body: `{"retry_after": 0.1}`})
	r.attention(2, "a", "b")
	r.fake.next(t)
	r.fake.next(t)
	if log := r.settle(); !strings.Contains(log, "trying again in 0.1 s") {
		t.Fatalf("log = %q", log)
	}

	// 5xx three times: three tries, then a log line; the plugin stays ready.
	before := r.fake.count()
	r.fake.answer(discordAnswer{status: 502}, discordAnswer{status: 503}, discordAnswer{status: 500})
	r.attention(3, "a", "b")
	r.waitLog("not sent: Discord answered with status 500 3 times")
	if n := r.fake.count() - before; n != 3 {
		t.Fatalf("%d tries, want 3", n)
	}
	if st := r.state(); st.State != plugins.StateReady {
		t.Fatalf("state = %s (%s)", st.State, st.Message)
	}

	// 404 is not retried; a long Retry-After is not waited for.
	before = r.fake.count()
	r.fake.answer(discordAnswer{status: 404, body: `{"message": "Unknown Webhook", "code": 10015}`})
	r.attention(4, "a", "b")
	r.waitLog("status 404: the webhook does not exist any more")
	r.fake.answer(discordAnswer{status: 429, headers: map[string]string{"Retry-After": "60"}})
	r.attention(5, "a", "b")
	r.waitLog("longer than this plugin waits")
	if n := r.fake.count() - before; n != 2 {
		t.Fatalf("%d tries, want one each", n)
	}

	// The webhook's token never reaches the log.
	for _, l := range r.m.LogLines("discord", plugins.LogLines) {
		if strings.Contains(l.Message, testWebhookToken) || strings.Contains(l.Message, testWebhookID) {
			t.Fatalf("log line %q holds the webhook address", l.Message)
		}
	}
}

func TestDiscordEventsAndCap(t *testing.T) {
	// Only the configured events send.
	r := newDiscordRun(t, map[string]any{"events": []any{"question_asked"}})
	r.attention(1, "a", "b")
	r.question(2, "q", "Which?", api.RoleUser)
	if b := r.fake.next(t); b["content"] != "A question for you" {
		t.Fatalf("body = %s", jsonText(b))
	}
	r.settle()
	if n := r.fake.count(); n != 1 {
		t.Fatalf("%d requests, want 1", n)
	}

	// At most max_per_hour messages, then one notice, then nothing.
	r = newDiscordRun(t, map[string]any{"max_per_hour": 2})
	for i := range 4 {
		r.attention(10+i, "a", "b")
	}
	r.waitHeld(2)
	r.settle()
	if n := r.fake.count(); n != 3 {
		t.Fatalf("%d requests, want 2 and the notice", n)
	}
	notice := r.fake.bodies[2]
	if c, _ := notice["content"].(string); !strings.HasPrefix(c, "omo sent 2 messages this hour") || notice["embeds"] != nil {
		t.Fatalf("notice = %s", jsonText(notice))
	}
}

func TestDiscordTestAction(t *testing.T) {
	r := newDiscordRun(t, map[string]any{"mention": "role:" + testRole})
	msg, err := r.m.Trigger(context.Background(), "discord", "test", nil)
	if err != nil || msg != "Sent a test message." {
		t.Fatalf("answer %q, %v", msg, err)
	}
	b := r.fake.bodies[0]
	if b["content"] != "<@&"+testRole+"> Test message from omo." || jsonText(b["allowed_mentions"]) != `{"parse":[],"roles":["`+testRole+`"]}` {
		t.Fatalf("body = %s", jsonText(b))
	}

	r.fake.answer(discordAnswer{status: 401})
	msg, _ = r.m.Trigger(context.Background(), "discord", "test", nil)
	if !strings.HasPrefix(msg, "Not sent: Discord answered with status 401") {
		t.Fatalf("answer %q", msg)
	}

	empty := newDiscordRun(t, map[string]any{"webhook_url": ""})
	msg, _ = empty.m.Trigger(context.Background(), "discord", "test", nil)
	if msg != "Not sent: set webhook_url in the plugin's configuration." || empty.fake.count() != 0 {
		t.Fatalf("answer %q", msg)
	}
}
