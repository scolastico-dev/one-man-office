package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/scolastico-dev/one-man-office/internal/agentsetup"
	"github.com/scolastico-dev/one-man-office/internal/api"
)

const (
	claudeFixtureToken = "sk-ant-oat01-FIXTURE-claude-access-token-0001"
	codexStaleToken    = "FIXTURE-codex-access-token-stale-0001"
	codexFreshToken    = "FIXTURE-codex-access-token-fresh-0001"
	codexAccount       = "fixture-account-0001"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// copyFixture copies testdata/<name> into a new folder and returns it.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.WriteFile(filepath.Join(dir, e.Name()), fixture(t, filepath.Join(name, e.Name())), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDefaults(t *testing.T) {
	d := Defaults()
	if len(d) != 3 || d["claude"] == "" || d["codex"] == "" || d["agy"] == "" {
		t.Fatalf("defaults = %v", slices.Collect(func(yield func(string) bool) {
			for k := range d {
				if !yield(k) {
					return
				}
			}
		}))
	}
	d["claude"] = "changed"
	if Defaults()["claude"] == "changed" {
		t.Fatal("Defaults returns a shared map")
	}
	for name, text := range Defaults() {
		if err := Check(name, text); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func claudeServer(t *testing.T, token string) *fakeAPI {
	return claudeServerWith(t, token, fixture(t, "claude-usage.json"))
}

// claudeServerWith answers the usage request with body.
func claudeServerWith(t *testing.T, token string, body []byte) *fakeAPI {
	return &fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "api.anthropic.com" || r.URL.Path != "/api/oauth/usage" || r.Method != http.MethodGet {
			t.Errorf("request to %s %s%s", r.Method, r.URL.Host, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}}
}

func claudeJob(p api.Profile) Job {
	p.CLI = api.CLIClaude
	if p.Command == "" {
		p.Command = "claude"
	}
	return Job{Script: "claude", Text: Defaults()["claude"], Profile: p}
}

// claudeWant is what the script reads from an answer without a limits
// list (testdata/claude-usage.json): the windows from five_hour,
// seven_day and seven_day_<model>.
var claudeWant = []Window{
	{Name: "session", Label: "Session (5 hours)", UsedPercent: 42.5, ResetsAt: time.Date(2026, 11, 15, 18, 0, 0, 123456000, time.UTC)},
	{Name: "weekly", Label: "Weekly, all models", UsedPercent: 12, ResetsAt: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)},
	{Name: "fable_weekly", Label: "Weekly, Fable", UsedPercent: 81, ResetsAt: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)},
}

// claudeLimitsWant is what the script reads from an answer with a limits
// list (testdata/claude-usage-limits.json, the shape the API sends since
// the per-model seven_day_<model> keys stay null).
var claudeLimitsWant = []Window{
	{Name: "session", Label: "Session (5 hours)", UsedPercent: 15, ResetsAt: time.Date(2026, 11, 16, 13, 40, 0, 193786000, time.UTC)},
	{Name: "weekly", Label: "Weekly, all models", UsedPercent: 85, ResetsAt: time.Date(2026, 11, 22, 4, 0, 0, 193803000, time.UTC)},
	{Name: "fable_weekly", Label: "Weekly, Fable", UsedPercent: 43, ResetsAt: time.Date(2026, 11, 22, 4, 0, 0, 193968000, time.UTC)},
	{Name: "extra_usage", Label: "Extra usage credits (1.32 of 5.00 EUR)", UsedPercent: 26.4},
}

// changedAnswer is the recorded limits answer with change applied to it.
func changedAnswer(t *testing.T, change func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(fixture(t, "claude-usage-limits.json"), &m); err != nil {
		t.Fatal(err)
	}
	change(m)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestClaudeDefaultScriptReadsLimits(t *testing.T) {
	cleanEnv(t)
	setGOOS(t, "linux")
	run := func(t *testing.T, body []byte) (Result, error) {
		t.Helper()
		return Run(context.Background(), claudeJob(api.Profile{AccountDir: copyFixture(t, "claude")}), RunOptions{Home: t.TempDir(), HTTPClient: claudeServerWith(t, claudeFixtureToken, body).client()})
	}
	limit := func(kind string, percent any, scope any) map[string]any {
		return map[string]any{"kind": kind, "group": "weekly", "percent": percent, "severity": "normal", "resets_at": "2026-11-22T04:00:00+00:00", "scope": scope, "is_active": false}
	}
	weekly := time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC)

	t.Run("recorded answer", func(t *testing.T) {
		res, err := run(t, fixture(t, "claude-usage-limits.json"))
		checkRun(t, res, err, "", claudeLimitsWant)
		if len(res.Log) != 0 {
			t.Fatalf("log = %q", res.Log)
		}
	})

	t.Run("unknown kinds and scoped limits without a model", func(t *testing.T) {
		body := changedAnswer(t, func(m map[string]any) {
			m["limits"] = append(m["limits"].([]any),
				limit("daily_magic", 5, nil),
				limit("weekly_scoped", 7, map[string]any{"model": nil, "surface": map[string]any{"id": nil, "display_name": "Claude Code"}}),
				limit("weekly_scoped", 8, map[string]any{"model": map[string]any{"id": "claude-x-1", "display_name": nil}, "surface": nil}),
				limit("weekly_scoped", 9, map[string]any{"model": nil, "surface": "Desktop App"}),
				limit("weekly_scoped", 10, map[string]any{"model": nil, "surface": nil}),
				limit("weekly_scoped", 11, nil),
				limit("weekly_scoped", 12, map[string]any{"model": map[string]any{"display_name": "Fable"}}),
				limit("weekly_all", "much", nil),
			)
		})
		res, err := run(t, body)
		want := append(slices.Clone(claudeLimitsWant[:3]),
			Window{Name: "surface_claude_code_weekly", Label: "Weekly, Claude Code", UsedPercent: 7, ResetsAt: weekly},
			Window{Name: "claude-x-1_weekly", Label: "Weekly, claude-x-1", UsedPercent: 8, ResetsAt: weekly},
			Window{Name: "surface_desktop_app_weekly", Label: "Weekly, Desktop App", UsedPercent: 9, ResetsAt: weekly},
			claudeLimitsWant[3])
		checkRun(t, res, err, "", want)
		wantLog := []string{
			"skipped the usage limit of unknown kind daily_magic",
			"skipped a weekly_scoped limit that names neither a model nor a surface",
			"skipped a weekly_scoped limit that names neither a model nor a surface",
			"skipped a second weekly_scoped limit for fable_weekly",
			"skipped a weekly_all limit without a percentage",
		}
		if !slices.Equal(res.Log, wantLog) {
			t.Fatalf("log = %q, want %q", res.Log, wantLog)
		}
	})

	t.Run("no limits list: the older keys", func(t *testing.T) {
		for name, change := range map[string]func(m map[string]any){
			"empty":   func(m map[string]any) { m["limits"] = []any{} },
			"missing": func(m map[string]any) { delete(m, "limits") },
			"null":    func(m map[string]any) { m["limits"] = nil },
		} {
			t.Run(name, func(t *testing.T) {
				res, err := run(t, changedAnswer(t, change))
				checkRun(t, res, err, "", []Window{claudeLimitsWant[0], claudeLimitsWant[1], claudeLimitsWant[3]})
			})
		}
	})

	t.Run("extra usage without amounts", func(t *testing.T) {
		res, err := run(t, changedAnswer(t, func(m map[string]any) {
			m["extra_usage"] = map[string]any{"is_enabled": true, "monthly_limit": nil, "used_credits": nil, "utilization": 10, "currency": nil}
		}))
		checkRun(t, res, err, "", append(slices.Clone(claudeLimitsWant[:3]), Window{Name: "extra_usage", Label: "Extra usage credits", UsedPercent: 10}))
	})

	t.Run("extra usage in whole units", func(t *testing.T) {
		res, err := run(t, changedAnswer(t, func(m map[string]any) {
			m["extra_usage"] = map[string]any{"monthly_limit": 50, "used_credits": 7, "utilization": 14, "currency": "USD", "decimal_places": 0}
		}))
		checkRun(t, res, err, "", append(slices.Clone(claudeLimitsWant[:3]), Window{Name: "extra_usage", Label: "Extra usage credits (7 of 50 USD)", UsedPercent: 14}))
	})

	t.Run("extra usage turned off", func(t *testing.T) {
		res, err := run(t, changedAnswer(t, func(m map[string]any) {
			m["extra_usage"] = map[string]any{"is_enabled": false, "monthly_limit": nil, "used_credits": nil, "utilization": nil}
		}))
		checkRun(t, res, err, "", claudeLimitsWant[:3])
	})
}

func TestClaudeDefaultScript(t *testing.T) {
	cleanEnv(t)
	setGOOS(t, "linux")

	t.Run("account folder", func(t *testing.T) {
		dir := copyFixture(t, "claude")
		srv := claudeServer(t, claudeFixtureToken)
		res, err := Run(context.Background(), claudeJob(api.Profile{AccountDir: dir}), RunOptions{Home: t.TempDir(), HTTPClient: srv.client()})
		checkRun(t, res, err, "", claudeWant)
		if hosts := srv.asked(); !slices.Equal(hosts, []string{"api.anthropic.com"}) {
			t.Fatalf("hosts = %v", hosts)
		}
	})

	t.Run("default folder", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), fixture(t, "claude/.credentials.json"), 0o600)
		res, err := Run(context.Background(), claudeJob(api.Profile{}), RunOptions{Home: home, HTTPClient: claudeServer(t, claudeFixtureToken).client()})
		checkRun(t, res, err, "", claudeWant)
	})

	t.Run("secure storage folder wins", func(t *testing.T) {
		secure := t.TempDir()
		other := `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-FIXTURE-secure-storage-0002"}}`
		os.WriteFile(filepath.Join(secure, ".credentials.json"), []byte(other), 0o600)
		p := api.Profile{AccountDir: copyFixture(t, "claude"), Env: map[string]string{"CLAUDE_SECURESTORAGE_CONFIG_DIR": secure}}
		res, err := Run(context.Background(), claudeJob(p), RunOptions{Home: t.TempDir(), HTTPClient: claudeServer(t, "sk-ant-oat01-FIXTURE-secure-storage-0002").client()})
		checkRun(t, res, err, "", claudeWant)
	})

	t.Run("empty secure storage folder means the default", func(t *testing.T) {
		home := t.TempDir()
		os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
		os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), fixture(t, "claude/.credentials.json"), 0o600)
		p := api.Profile{AccountDir: t.TempDir(), Env: map[string]string{"CLAUDE_SECURESTORAGE_CONFIG_DIR": ""}}
		res, err := Run(context.Background(), claudeJob(p), RunOptions{Home: home, HTTPClient: claudeServer(t, claudeFixtureToken).client()})
		checkRun(t, res, err, "", claudeWant)
	})

	t.Run("missing token", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"refreshToken":"sk-ant-ort01-FIXTURE-only-refresh"}}`), 0o600)
		_, err := Run(context.Background(), claudeJob(api.Profile{AccountDir: dir}), RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t)})
		if err == nil || err.Error() != "the Claude credentials hold no OAuth access token" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		_, err := Run(context.Background(), claudeJob(api.Profile{AccountDir: dir}), RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t)})
		if err == nil || err.Error() != "no Claude credentials: file not found" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("refused token", func(t *testing.T) {
		dir := copyFixture(t, "claude")
		_, err := Run(context.Background(), claudeJob(api.Profile{AccountDir: dir}), RunOptions{Home: t.TempDir(), HTTPClient: claudeServer(t, "another").client()})
		if err == nil || err.Error() != "usage API returned HTTP 401" {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestClaudeKeychainOnDarwinOnly(t *testing.T) {
	cleanEnv(t)
	var services []string
	keychain := func(_ context.Context, service string) (string, error) {
		services = append(services, service)
		return string(fixture(t, "claude/.credentials.json")), nil
	}

	setGOOS(t, "darwin")
	dir := t.TempDir() // no .credentials.json in it
	res, err := Run(context.Background(), claudeJob(api.Profile{AccountDir: dir}),
		RunOptions{Home: t.TempDir(), HTTPClient: claudeServer(t, claudeFixtureToken).client(), Keychain: keychain})
	checkRun(t, res, err, "", claudeWant)
	sum := sha256.Sum256([]byte(norm.NFC.String(dir)))
	if want := "Claude Code-credentials-" + hex.EncodeToString(sum[:])[:8]; !slices.Equal(services, []string{want}) {
		t.Fatalf("services = %q, want %q", services, want)
	}

	services = nil
	res, err = Run(context.Background(), claudeJob(api.Profile{}),
		RunOptions{Home: t.TempDir(), HTTPClient: claudeServer(t, claudeFixtureToken).client(), Keychain: keychain})
	checkRun(t, res, err, "", claudeWant)
	if !slices.Equal(services, []string{"Claude Code-credentials"}) {
		t.Fatalf("services = %q", services)
	}

	setGOOS(t, "linux")
	services = nil
	_, err = Run(context.Background(), claudeJob(api.Profile{AccountDir: dir}),
		RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t), Keychain: keychain})
	if err == nil || err.Error() != "no Claude credentials: file not found" || len(services) != 0 {
		t.Fatalf("err = %v, services = %q", err, services)
	}
}

func codexServer(t *testing.T, fresh string) *fakeAPI {
	return &fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "chatgpt.com" || r.URL.Path != "/backend-api/wham/usage" {
			t.Errorf("request to %s%s", r.URL.Host, r.URL.Path)
		}
		if r.Header.Get("ChatGPT-Account-Id") != codexAccount {
			t.Errorf("account header = %q", r.Header.Get("ChatGPT-Account-Id"))
		}
		if r.Header.Get("Authorization") != "Bearer "+fresh {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"token expired"}`)
			return
		}
		w.Write(fixture(t, "codex-usage.json"))
	}}
}

func codexJob(dir string) Job {
	return Job{Script: "codex", Text: Defaults()["codex"], Profile: api.Profile{Name: "codex", CLI: api.CLICodex, Command: "/opt/codex", AccountDir: dir}}
}

var codexWant = []Window{
	{Name: "session", Label: "Session", UsedPercent: 7, ResetsAt: time.Unix(1800003600, 0).UTC()},
	{Name: "weekly", Label: "Weekly", UsedPercent: 40, ResetsAt: time.Unix(1800500000, 0).UTC()},
	{Name: "gpt-5_3-codex-spark_session", Label: "Session, GPT-5.3-Codex-Spark", UsedPercent: 0, ResetsAt: time.Unix(1800003600, 0).UTC()},
	{Name: "gpt-5_3-codex-spark_weekly", Label: "Weekly, GPT-5.3-Codex-Spark", UsedPercent: 63, ResetsAt: time.Unix(1800600000, 0).UTC()},
}

// rewriteAuth stands in for the Codex CLI refreshing its login.
func rewriteAuth(token string, calls *int, commands *[]string) func(context.Context, string, []string, string) error {
	return func(_ context.Context, command string, _ []string, path string) error {
		*calls++
		*commands = append(*commands, command)
		body := `{"tokens":{"access_token":"` + token + `","account_id":"` + codexAccount + `"}}`
		return os.WriteFile(path, []byte(body), 0o600)
	}
}

func TestCodexDefaultScriptRefreshesOnce(t *testing.T) {
	cleanEnv(t)

	t.Run("valid token", func(t *testing.T) {
		dir := copyFixture(t, "codex")
		calls, commands := 0, []string{}
		srv := codexServer(t, codexStaleToken)
		res, err := Run(context.Background(), codexJob(dir), RunOptions{Home: t.TempDir(), HTTPClient: srv.client(), RefreshCodex: rewriteAuth(codexFreshToken, &calls, &commands)})
		checkRun(t, res, err, "", codexWant)
		if calls != 0 || !slices.Equal(srv.asked(), []string{"chatgpt.com"}) {
			t.Fatalf("calls = %d, hosts = %v", calls, srv.asked())
		}
	})

	t.Run("401 then refreshed", func(t *testing.T) {
		dir := copyFixture(t, "codex")
		calls, commands := 0, []string{}
		srv := codexServer(t, codexFreshToken)
		res, err := Run(context.Background(), codexJob(dir), RunOptions{Home: t.TempDir(), HTTPClient: srv.client(), RefreshCodex: rewriteAuth(codexFreshToken, &calls, &commands)})
		checkRun(t, res, err, "", codexWant)
		if calls != 1 || !slices.Equal(commands, []string{"/opt/codex"}) || len(srv.asked()) != 2 {
			t.Fatalf("calls = %d, commands = %q, hosts = %v", calls, commands, srv.asked())
		}
	})

	t.Run("401 twice", func(t *testing.T) {
		dir := copyFixture(t, "codex")
		calls, commands := 0, []string{}
		srv := codexServer(t, "never-matches-anything")
		_, err := Run(context.Background(), codexJob(dir), RunOptions{Home: t.TempDir(), HTTPClient: srv.client(), RefreshCodex: rewriteAuth("FIXTURE-codex-still-stale-0003", &calls, &commands)})
		if err == nil || err.Error() != "usage API returned HTTP 401" || calls != 1 {
			t.Fatalf("err = %v, calls = %d", err, calls)
		}
	})

	t.Run("refresh fails", func(t *testing.T) {
		dir := copyFixture(t, "codex")
		srv := codexServer(t, codexFreshToken)
		_, err := Run(context.Background(), codexJob(dir), RunOptions{Home: t.TempDir(), HTTPClient: srv.client(),
			RefreshCodex: func(context.Context, string, []string, string) error { return errors.New("exited") }})
		if err == nil || err.Error() != "the Codex login was refused and could not be refreshed: codex refresh failed" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("default folder", func(t *testing.T) {
		home := t.TempDir()
		os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
		os.WriteFile(filepath.Join(home, ".codex", "auth.json"), fixture(t, "codex/auth.json"), 0o600)
		res, err := Run(context.Background(), codexJob(""), RunOptions{Home: home, HTTPClient: codexServer(t, codexStaleToken).client()})
		checkRun(t, res, err, "", codexWant)
	})

	t.Run("primary weekly only", func(t *testing.T) {
		dir := copyFixture(t, "codex")
		srv := &fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"rate_limit":{"primary_window":{"used_percent":7,"limit_window_seconds":604800,"reset_at":1800000000},"secondary_window":null}}`)
		}}
		res, err := Run(context.Background(), codexJob(dir), RunOptions{Home: t.TempDir(), HTTPClient: srv.client()})
		checkRun(t, res, err, "", []Window{{Name: "weekly", Label: "Weekly", UsedPercent: 7, ResetsAt: time.Unix(1800000000, 0).UTC()}})
	})
}

// TestDefaultScriptsKeepTokensInTheRun runs both shipped scripts against
// servers that echo the credentials back in an error answer, through the
// tracker, and looks for the tokens in everything that leaves: the error
// text, the log, the tracker's state (what events and GET /api/usage
// carry), its log and the kept readings file.
func TestDefaultScriptsKeepTokensInTheRun(t *testing.T) {
	cleanEnv(t)
	setGOOS(t, "linux")
	claudeDir := copyFixture(t, "claude")
	codexDir := copyFixture(t, "codex")
	secrets := []string{claudeFixtureToken, "sk-ant-ort01-FIXTURE-claude-refresh-token-0001", codexStaleToken, codexAccount, "FIXTURE-codex-refresh-token-0001", "FIXTURE-codex-id-token-0001"}
	echo := &fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "got "+r.Header.Get("Authorization")+" "+r.Header.Get("ChatGPT-Account-Id"))
	}}
	leaky := strings.NewReplacer("error(\"usage API returned HTTP \" .. res.status, 0)", "error(\"HTTP \" .. res.status .. \": \" .. res.body, 0)")
	src := newSource(
		api.Profile{Name: "claude", CLI: api.CLIClaude, Command: "claude", UsageScript: "claude", AccountDir: claudeDir, Models: map[string]api.ProfileModel{"opus": {}}},
		api.Profile{Name: "codex", CLI: api.CLICodex, Command: "codex", UsageScript: "codex", AccountDir: codexDir, Models: map[string]api.ProfileModel{"default": {}}},
	)
	src.scripts = map[string]string{"claude": leaky.Replace(Defaults()["claude"]), "codex": leaky.Replace(Defaults()["codex"])}
	if src.scripts["claude"] == Defaults()["claude"] || src.scripts["codex"] == Defaults()["codex"] {
		t.Fatal("the scripts were not made to leak")
	}
	var logged strings.Builder
	var mu sync.Mutex
	statePath := filepath.Join(t.TempDir(), "usage.json")
	run := NewRunFunc(RunOptions{Home: t.TempDir(), HTTPClient: echo.client(),
		RefreshCodex: func(context.Context, string, []string, string) error { return nil }})
	var errs []string
	tr := NewTracker(TrackerOptions{Source: src, StatePath: statePath, Log: log.New(lockedWriter{&mu, &logged}, "", 0),
		Run: func(ctx context.Context, j Job) (Result, error) {
			res, err := run(ctx, j)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err.Error())
			}
			errs = append(errs, res.Log...)
			return res, err
		}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	state := tr.Refresh(ctx)
	raw, _ := json.Marshal(state)
	kept, _ := os.ReadFile(statePath)
	mu.Lock()
	defer mu.Unlock()
	out := strings.Join(errs, "\n") + "\n" + string(raw) + "\n" + logged.String() + "\n" + string(kept)
	if !strings.Contains(out, "HTTP 400: got [redacted]") {
		t.Fatalf("the leaking scripts did not run as expected:\n%s", out)
	}
	for _, s := range secrets {
		if strings.Contains(out, s) {
			t.Fatalf("secret %q left the run:\n%s", s, out)
		}
	}
}

// Both shipped scripts end at an answer 429 with the rate-limit error, ask
// once only, and pass the answer's Retry-After on.
func TestDefaultScriptsStopAtRateLimit(t *testing.T) {
	cleanEnv(t)
	setGOOS(t, "linux")
	for _, c := range []struct {
		name  string
		job   func() Job
		after string
		want  time.Duration
	}{
		{"claude", func() Job { return claudeJob(api.Profile{AccountDir: copyFixture(t, "claude")}) }, "120", 2 * time.Minute},
		{"claude without Retry-After", func() Job { return claudeJob(api.Profile{AccountDir: copyFixture(t, "claude")}) }, "", 0},
		{"codex", func() Job { return codexJob(copyFixture(t, "codex")) }, "30", 30 * time.Second},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := &fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) {
				if c.after != "" {
					w.Header().Set("Retry-After", c.after)
				}
				w.WriteHeader(http.StatusTooManyRequests)
			}}
			_, err := Run(context.Background(), c.job(), RunOptions{Home: t.TempDir(), HTTPClient: srv.client(),
				RefreshCodex: func(context.Context, string, []string, string) error { t.Error("refresh after 429"); return nil }})
			var limited *RateLimitError
			if !errors.As(err, &limited) || err.Error() != "the usage API is rate limited; trying again later" || limited.RetryAfter != c.want {
				t.Fatalf("err = %v (%#v)", err, limited)
			}
			if n := len(srv.asked()); n != 1 {
				t.Fatalf("%d requests after a 429, want 1", n)
			}
		})
	}
}

// The windows the shipped Claude script reads from today's answer count for
// the catalog's Claude models as listed: fable_weekly for claude/fable and
// for no other model, extra_usage for none, and no model gets the note that
// none of its windows was reported.
func TestClaudeLimitsCountForTheCatalogModels(t *testing.T) {
	cleanEnv(t)
	setGOOS(t, "linux")
	catalog, ok := agentsetup.Catalog(api.CLIClaude)
	if !ok {
		t.Fatal("no Claude catalog entry")
	}
	for name, m := range catalog.Models {
		if lists := slices.Contains(m.UsageWindows, "fable_weekly"); lists != (name == "fable") {
			t.Fatalf("claude/%s lists %v", name, m.UsageWindows)
		}
	}
	judge := func(t *testing.T, body []byte) map[string]api.ModelUsage {
		t.Helper()
		res, err := Run(context.Background(), claudeJob(api.Profile{AccountDir: copyFixture(t, "claude")}), RunOptions{Home: t.TempDir(), HTTPClient: claudeServerWith(t, claudeFixtureToken, body).client()})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
		ws, err := cleanWindows(res.Windows, now)
		if err != nil {
			t.Fatal(err)
		}
		r := &api.UsageReading{Windows: ws, FetchedAt: &now}
		out := map[string]api.ModelUsage{}
		for name, m := range catalog.Models {
			out[name] = Evaluate("claude/"+name, m.UsageWindows, r, th(90, 98), now)
		}
		return out
	}
	check := func(t *testing.T, got map[string]api.ModelUsage, want map[string][2]string) {
		t.Helper()
		for name, w := range want {
			if g := got[name]; g.State != w[0] || g.Window != w[1] || g.Note != "" {
				t.Errorf("claude/%s = %+v, want %s by %s", name, g, w[0], w[1])
			}
		}
	}

	t.Run("recorded answer", func(t *testing.T) {
		// weekly 85 % is the fullest window of every model; fable_weekly
		// (43 %) counts for fable, extra_usage (26.4 %) for none.
		check(t, judge(t, fixture(t, "claude-usage-limits.json")), map[string][2]string{
			"fable": {api.UsageOK, "weekly"}, "opus": {api.UsageOK, "weekly"}, "sonnet": {api.UsageOK, "weekly"}, "haiku": {api.UsageOK, "weekly"},
		})
	})

	t.Run("fable near its own limit", func(t *testing.T) {
		body := changedAnswer(t, func(m map[string]any) {
			m["limits"].([]any)[2].(map[string]any)["percent"] = 95
			m["extra_usage"].(map[string]any)["utilization"] = 100
		})
		check(t, judge(t, body), map[string][2]string{
			"fable": {api.UsageSoft, "fable_weekly"}, "opus": {api.UsageOK, "weekly"}, "sonnet": {api.UsageOK, "weekly"}, "haiku": {api.UsageOK, "weekly"},
		})
	})

	t.Run("fable at its own limit", func(t *testing.T) {
		body := changedAnswer(t, func(m map[string]any) { m["limits"].([]any)[2].(map[string]any)["percent"] = 99 })
		got := judge(t, body)
		check(t, got, map[string][2]string{"fable": {api.UsageHard, "fable_weekly"}, "opus": {api.UsageOK, "weekly"}})
		if got["fable"].ResetsAt == nil || !got["fable"].ResetsAt.Equal(time.Date(2026, 11, 22, 4, 0, 0, 193968000, time.UTC)) {
			t.Fatalf("fable resets %v", got["fable"].ResetsAt)
		}
	})
}

// codexFixtureRun runs the shipped Codex script against a fixture answer.
func codexFixtureRun(t *testing.T, name string) (Result, error) {
	t.Helper()
	cleanEnv(t)
	srv := &fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) { w.Write(fixture(t, name)) }}
	return Run(context.Background(), codexJob(copyFixture(t, "codex")), RunOptions{Home: t.TempDir(), HTTPClient: srv.client()})
}

func TestCodexDefaultScriptNamesWindowsByLength(t *testing.T) {
	res, err := codexFixtureRun(t, "codex-usage-other-windows.json")
	checkRun(t, res, err, "", []Window{
		{Name: "window_24h", Label: "24 hours", UsedPercent: 12, ResetsAt: time.Unix(1800003600, 0).UTC()},
		{Name: "window_336h", Label: "336 hours", UsedPercent: 30, ResetsAt: time.Unix(1800500000, 0).UTC()},
		{Name: "extra-model_session", Label: "Session, Extra-Model", UsedPercent: 5, ResetsAt: time.Unix(1800003600, 0).UTC()},
		{Name: "extra-model_weekly", Label: "Weekly, Extra-Model", UsedPercent: 9, ResetsAt: time.Unix(1800600000, 0).UTC()},
	})
}

func TestCodexDefaultScriptReadsResetTimes(t *testing.T) {
	res, err := codexFixtureRun(t, "codex-usage-reset-times.json")
	checkRun(t, res, err, "", []Window{
		{Name: "session", Label: "Session", UsedPercent: 1, ResetsAt: time.Date(2027, 1, 15, 8, 0, 0, 0, time.UTC)},
		{Name: "weekly", Label: "Weekly", UsedPercent: 2, ResetsAt: time.Date(2027, 1, 16, 8, 30, 0, 250000000, time.UTC)},
		{Name: "odd_session", Label: "Session, Odd", UsedPercent: 3},
		{Name: "odd_weekly", Label: "Weekly, Odd", UsedPercent: 4},
	})
	if len(res.Log) != 2 {
		t.Fatalf("log = %q, want one line for each reset time that cannot be read", res.Log)
	}
}

func TestCodexDefaultScriptOddShapes(t *testing.T) {
	run := func(t *testing.T, body string) (Result, error) {
		t.Helper()
		cleanEnv(t)
		srv := &fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }}
		return Run(context.Background(), codexJob(copyFixture(t, "codex")), RunOptions{Home: t.TempDir(), HTTPClient: srv.client()})
	}
	t.Run("one window of another length", func(t *testing.T) {
		res, err := run(t, `{"rate_limit":{"primary_window":{"used_percent":7,"limit_window_seconds":18000}}}`)
		checkRun(t, res, err, "", []Window{{Name: "session", Label: "Session", UsedPercent: 7}})
	})
	t.Run("an hour and a half", func(t *testing.T) {
		res, err := run(t, `{"rate_limit":{"primary_window":{"used_percent":7,"limit_window_seconds":1800}}}`)
		checkRun(t, res, err, "", []Window{{Name: "window_1h", Label: "1 hour", UsedPercent: 7}})
	})
	t.Run("both windows alike: the secondary stays", func(t *testing.T) {
		res, err := run(t, `{"rate_limit":{"primary_window":{"used_percent":7,"limit_window_seconds":604800},"secondary_window":{"used_percent":8,"limit_window_seconds":604800}}}`)
		checkRun(t, res, err, "", []Window{{Name: "weekly", Label: "Weekly", UsedPercent: 8}})
		if len(res.Log) != 1 {
			t.Fatalf("log = %q", res.Log)
		}
	})
	t.Run("a reset time in milliseconds is logged, not fatal", func(t *testing.T) {
		res, err := run(t, `{"rate_limit":{"primary_window":{"used_percent":7,"limit_window_seconds":18000,"reset_at":1800003600000}}}`)
		checkRun(t, res, err, "", []Window{{Name: "session", Label: "Session", UsedPercent: 7}})
		if len(res.Log) != 1 {
			t.Fatalf("log = %q", res.Log)
		}
	})
	t.Run("no window at all", func(t *testing.T) {
		_, err := run(t, `{"rate_limit":{"primary_window":{"limit_window_seconds":18000}}}`)
		if err == nil || err.Error() != "the usage answer has no valid window" {
			t.Fatalf("err = %v", err)
		}
	})
}
