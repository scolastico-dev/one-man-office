package modelusage

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countingFetcher struct{ calls int }

func (f *countingFetcher) Fetch(context.Context, string, config.Profile) (Snapshot, error) {
	f.calls++
	return Snapshot{UsedPercent: 10}, nil
}

func usageHTTPClient(t *testing.T, status int, body string, check func(*http.Request)) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if check != nil {
			check(r)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
}

func TestCodexFetchUsesNativeCredentialsAndAccountWeeklyWindow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "auth.json"), []byte(`{"tokens":{"access_token":"secret-token","account_id":"acct-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	httpClient := usageHTTPClient(t, http.StatusOK, `{"rate_limit":{"secondary_window":{"used_percent":40,"reset_at":1800000000}},"additional_rate_limits":[{"limit_name":"codex-luna","rate_limit":{"secondary_window":{"used_percent":63}}}]}`, func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" || r.Header.Get("ChatGPT-Account-Id") != "acct-1" {
			t.Fatalf("unexpected headers: %#v", r.Header)
		}
	})
	client := Client{HTTPClient: httpClient, CodexURL: "https://usage.test/codex"}
	snapshot, err := client.Fetch(context.Background(), "luna", config.Profile{
		Cmd: "codex", Args: []string{"--model", "codex-luna"}, Env: map[string]string{"CODEX_HOME": root},
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Provider != agentcli.Codex || snapshot.UsedPercent != 40 || snapshot.Scope == "" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestCodexFetchAcceptsWeeklyPrimaryWindowWhenSecondaryIsMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "auth.json"), []byte(`{"tokens":{"access_token":"secret-token","account_id":"acct-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	httpClient := usageHTTPClient(t, http.StatusOK, `{
		"plan_type":"prolite",
		"rate_limit":{
			"primary_window":{"used_percent":7,"limit_window_seconds":604800,"reset_at":1787924560},
			"secondary_window":null
		},
		"additional_rate_limits":[{
			"limit_name":"GPT-5.3-Codex-Spark",
			"rate_limit":{
				"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_at":1787477399},
				"secondary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_at":1788064199}
			}
		}]
	}`, nil)
	client := Client{HTTPClient: httpClient, CodexURL: "https://usage.test/codex"}
	snapshot, err := client.Fetch(context.Background(), "codex-sol", config.Profile{
		Cmd: "codex", Args: []string{"--model", "gpt-5.6-sol"}, Env: map[string]string{"CODEX_HOME": root},
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.UsedPercent != 7 || snapshot.ResetAt.Unix() != 1787924560 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestPreflightSkipsAllUsageCallsWhenDisabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.Usage.Enabled = false
	cfg.Models = map[string]config.Profile{"codex": {Cmd: "codex"}}
	cfg.Roles = map[string]config.RoleModels{"ceo": {Models: []string{"codex"}}}
	fetcher := &countingFetcher{}
	if err := Preflight(context.Background(), &cfg, fetcher); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("disabled usage preflight made %d calls", fetcher.calls)
	}
}

func TestClaudeFetchUsesOAuthUsageEndpointAndWeeklyWindow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"claude-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	httpClient := usageHTTPClient(t, http.StatusOK, `{"five_hour":{"utilization":0.81,"resets_at":"2026-08-24T15:00:00Z"},"seven_day":{"utilization":0.52,"resets_at":"2026-08-25T00:00:00Z"},"seven_day_opus":{"utilization":0.70}}`, func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer claude-secret" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Fatalf("unexpected headers: %#v", r.Header)
		}
	})
	client := Client{HTTPClient: httpClient, ClaudeURL: "https://usage.test/claude"}
	snapshot, err := client.Fetch(context.Background(), "opus", config.Profile{
		Cmd: "claude", Args: []string{"--model", "claude-opus"}, Env: map[string]string{"CLAUDE_CONFIG_DIR": root},
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Provider != agentcli.Claude || snapshot.UsedPercent != 52 || !snapshot.HasSession || snapshot.SessionUsedPercent != 81 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if window, used := snapshot.LimitingWindow(); window != "session" || used != 81 {
		t.Fatalf("limiting window = %s %.1f", window, used)
	}
}

func TestPreflightFetchesOncePerCredentialScope(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models = map[string]config.Profile{
		"claude-a": {Cmd: "claude", Args: []string{"--model", "a"}, Env: map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/shared-claude"}},
		"claude-b": {Cmd: "claude", Args: []string{"--model", "b"}, Env: map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/shared-claude"}},
	}
	cfg.Roles = map[string]config.RoleModels{
		"ceo":       {Models: []string{"claude-a", "claude-b"}},
		"developer": {Models: []string{"claude-b"}},
	}
	fetcher := &countingFetcher{}
	if err := Preflight(context.Background(), &cfg, fetcher); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("preflight calls = %d, want one shared Claude request", fetcher.calls)
	}
}

func TestClaudeFetchRequiresSessionWindow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"claude-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	httpClient := usageHTTPClient(t, http.StatusOK, `{"seven_day":{"utilization":0.52}}`, nil)
	_, err := (Client{HTTPClient: httpClient, ClaudeURL: "https://usage.test/claude"}).Fetch(context.Background(), "opus", config.Profile{Cmd: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": root}})
	if err == nil || !strings.Contains(err.Error(), "five_hour") {
		t.Fatalf("missing session error = %v", err)
	}
}

func TestUsageErrorsDoNotExposeResponseBodies(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "auth.json"), []byte(`{"tokens":{"access_token":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	httpClient := usageHTTPClient(t, http.StatusUnauthorized, "sensitive provider detail", nil)
	_, err := (Client{HTTPClient: httpClient, CodexURL: "https://usage.test/codex"}).Fetch(context.Background(), "codex", config.Profile{Cmd: "codex", Env: map[string]string{"CODEX_HOME": root}})
	if err == nil || strings.Contains(err.Error(), "sensitive provider detail") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe error = %v", err)
	}
}
