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
	if len(d) != 2 || d["claude"] == "" || d["codex"] == "" {
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
	return &fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "api.anthropic.com" || r.URL.Path != "/api/oauth/usage" || r.Method != http.MethodGet {
			t.Errorf("request to %s %s%s", r.Method, r.URL.Host, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "claude-usage.json"))
	}}
}

func claudeJob(p api.Profile) Job {
	p.CLI = api.CLIClaude
	if p.Command == "" {
		p.Command = "claude"
	}
	return Job{Script: "claude", Text: Defaults()["claude"], Profile: p}
}

var claudeWant = []Window{
	{Name: "session", UsedPercent: 42.5, ResetsAt: time.Date(2026, 11, 15, 18, 0, 0, 123456000, time.UTC)},
	{Name: "weekly", UsedPercent: 12, ResetsAt: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)},
	{Name: "fable_weekly", UsedPercent: 81, ResetsAt: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)},
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
	{Name: "session", UsedPercent: 7, ResetsAt: time.Unix(1800003600, 0).UTC()},
	{Name: "weekly", UsedPercent: 40, ResetsAt: time.Unix(1800500000, 0).UTC()},
	{Name: "gpt-5_3-codex-spark_weekly", UsedPercent: 63, ResetsAt: time.Unix(1800600000, 0).UTC()},
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
		checkRun(t, res, err, "", []Window{{Name: "weekly", UsedPercent: 7, ResetsAt: time.Unix(1800000000, 0).UTC()}})
	})

	t.Run("no weekly window", func(t *testing.T) {
		dir := copyFixture(t, "codex")
		srv := &fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"rate_limit":{"primary_window":{"used_percent":7,"limit_window_seconds":18000}}}`)
		}}
		_, err := Run(context.Background(), codexJob(dir), RunOptions{Home: t.TempDir(), HTTPClient: srv.client()})
		if err == nil || err.Error() != "the usage answer has no valid weekly window" {
			t.Fatalf("err = %v", err)
		}
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
