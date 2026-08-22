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

func usageHTTPClient(t *testing.T, status int, body string, check func(*http.Request)) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if check != nil {
			check(r)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
}

func TestCodexFetchUsesNativeCredentialsAndMostConstrainedWindow(t *testing.T) {
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
	if snapshot.Provider != agentcli.Codex || snapshot.UsedPercent != 63 || snapshot.Scope == "" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestClaudeFetchUsesOAuthUsageEndpointAndWeeklyWindow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"claude-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	httpClient := usageHTTPClient(t, http.StatusOK, `{"seven_day":{"utilization":0.52,"resets_at":"2026-08-25T00:00:00Z"},"seven_day_opus":{"utilization":0.70}}`, func(r *http.Request) {
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
	if snapshot.Provider != agentcli.Claude || snapshot.UsedPercent != 70 {
		t.Fatalf("snapshot = %+v", snapshot)
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
