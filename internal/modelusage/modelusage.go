// Package modelusage reads native Claude/Codex credentials and fetches their
// subscription usage windows without refreshing or modifying credentials.
package modelusage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
)

const (
	defaultCodexURL  = "https://chatgpt.com/backend-api/wham/usage"
	defaultClaudeURL = "https://api.anthropic.com/api/oauth/usage"
)

type Snapshot struct {
	Provider    agentcli.Provider
	Scope       string
	UsedPercent float64
	ResetAt     time.Time
	FetchedAt   time.Time
}

type Fetcher interface {
	Fetch(context.Context, string, config.Profile) (Snapshot, error)
}

type Client struct {
	HTTPClient *http.Client
	CodexURL   string
	ClaudeURL  string
}

func (c Client) Fetch(ctx context.Context, profileKey string, profile config.Profile) (Snapshot, error) {
	provider := agentcli.Resolve(profile.Provider, profile.Cmd)
	switch provider {
	case agentcli.Codex:
		return c.fetchCodex(ctx, profileKey, profile)
	case agentcli.Claude:
		return c.fetchClaude(ctx, profileKey, profile)
	default:
		return Snapshot{}, fmt.Errorf("profile %q uses unsupported usage provider %q", profileKey, provider)
	}
}

func Metered(profile config.Profile) bool {
	provider := agentcli.Resolve(profile.Provider, profile.Cmd)
	return provider == agentcli.Claude || provider == agentcli.Codex
}

func Scope(profile config.Profile) string {
	provider := agentcli.Resolve(profile.Provider, profile.Cmd)
	switch provider {
	case agentcli.Codex:
		return string(provider) + ":" + filepath.Clean(filepath.Join(configRoot(profile.Env, "CODEX_HOME", ".codex"), "auth.json"))
	case agentcli.Claude:
		return string(provider) + ":" + filepath.Clean(filepath.Join(configRoot(profile.Env, "CLAUDE_CONFIG_DIR", ".claude"), ".credentials.json"))
	default:
		return ""
	}
}

func Preflight(ctx context.Context, cfg *config.Config, fetcher Fetcher) error {
	if !cfg.Usage.Enabled {
		return nil
	}
	seen := map[string][]string{}
	profiles := map[string]config.Profile{}
	for _, role := range config.AllRoles {
		for _, key := range cfg.Roles[role].Models {
			profile := cfg.Models[key]
			if !Metered(profile) {
				continue
			}
			scope := Scope(profile)
			seen[scope] = append(seen[scope], key)
			profiles[scope] = profile
		}
	}
	for scope, keys := range seen {
		if _, err := fetcher.Fetch(ctx, keys[0], profiles[scope]); err != nil {
			return fmt.Errorf("usage preflight for profiles %s: %w", strings.Join(keys, ", "), err)
		}
	}
	return nil
}

func (c Client) fetchCodex(ctx context.Context, profileKey string, profile config.Profile) (Snapshot, error) {
	path := filepath.Join(configRoot(profile.Env, "CODEX_HOME", ".codex"), "auth.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("codex credentials for profile %q: %w", profileKey, err)
	}
	var auth map[string]any
	if err := json.Unmarshal(raw, &auth); err != nil {
		return Snapshot{}, fmt.Errorf("codex credentials for profile %q are invalid: %w", profileKey, err)
	}
	tokens, _ := auth["tokens"].(map[string]any)
	access := firstString(tokens, "access_token", "accessToken")
	account := firstString(tokens, "account_id", "accountId")
	if access == "" {
		return Snapshot{}, fmt.Errorf("codex credentials for profile %q contain no OAuth access token", profileKey)
	}
	endpoint := c.CodexURL
	if endpoint == "" {
		endpoint = defaultCodexURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Snapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	if account != "" {
		req.Header.Set("ChatGPT-Account-Id", account)
	}
	var payload struct {
		RateLimit  rateLimitWindows `json:"rate_limit"`
		Additional []struct {
			Name      string           `json:"limit_name"`
			RateLimit rateLimitWindows `json:"rate_limit"`
		} `json:"additional_rate_limits"`
	}
	if err := c.doJSON(req, &payload); err != nil {
		return Snapshot{}, fmt.Errorf("codex usage for profile %q: %w", profileKey, err)
	}
	weekly := payload.RateLimit.weekly()
	if weekly == nil || !validPercent(weekly.UsedPercent) {
		return Snapshot{}, fmt.Errorf("codex usage for profile %q has no valid weekly rate-limit window", profileKey)
	}
	used := weekly.UsedPercent
	model := selectedModel(profile.Args)
	for _, limit := range payload.Additional {
		modelWeekly := limit.RateLimit.weekly()
		if model != "" && modelWeekly != nil && strings.Contains(strings.ToLower(limit.Name), strings.ToLower(model)) {
			if !validPercent(modelWeekly.UsedPercent) {
				return Snapshot{}, fmt.Errorf("codex usage for profile %q has an invalid model weekly window", profileKey)
			}
			used = max(used, modelWeekly.UsedPercent)
		}
	}
	return Snapshot{Provider: agentcli.Codex, Scope: Scope(profile), UsedPercent: used, ResetAt: unixTime(weekly.ResetAt), FetchedAt: time.Now()}, nil
}

func (c Client) fetchClaude(ctx context.Context, profileKey string, profile config.Profile) (Snapshot, error) {
	path := filepath.Join(configRoot(profile.Env, "CLAUDE_CONFIG_DIR", ".claude"), ".credentials.json")
	raw, err := readClaudeCredentials(ctx, path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("claude credentials for profile %q: %w", profileKey, err)
	}
	var credentials struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &credentials); err != nil {
		return Snapshot{}, fmt.Errorf("claude credentials for profile %q are invalid: %w", profileKey, err)
	}
	if credentials.OAuth.AccessToken == "" {
		return Snapshot{}, fmt.Errorf("claude credentials for profile %q contain no OAuth access token", profileKey)
	}
	endpoint := c.ClaudeURL
	if endpoint == "" {
		endpoint = defaultClaudeURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Snapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+credentials.OAuth.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	var payload map[string]json.RawMessage
	if err := c.doJSON(req, &payload); err != nil {
		return Snapshot{}, fmt.Errorf("claude usage for profile %q: %w", profileKey, err)
	}
	weekly, ok := payload["seven_day"]
	if !ok {
		return Snapshot{}, fmt.Errorf("claude usage for profile %q has no seven_day window", profileKey)
	}
	base, err := decodeClaudeWindow(weekly)
	if err != nil {
		return Snapshot{}, fmt.Errorf("claude usage for profile %q has invalid seven_day window: %w", profileKey, err)
	}
	used := base.UsedPercent
	model := strings.ToLower(selectedModel(profile.Args))
	for key, rawWindow := range payload {
		if key == "seven_day" || !strings.HasPrefix(key, "seven_day_") || model == "" || !strings.Contains(model, strings.TrimPrefix(key, "seven_day_")) {
			continue
		}
		if candidate, err := decodeClaudeWindow(rawWindow); err == nil {
			used = max(used, candidate.UsedPercent)
		}
	}
	return Snapshot{Provider: agentcli.Claude, Scope: Scope(profile), UsedPercent: used, ResetAt: base.ResetAt, FetchedAt: time.Now()}, nil
}

type window struct {
	UsedPercent        float64 `json:"used_percent"`
	ResetAt            int64   `json:"reset_at"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
}

type rateLimitWindows struct {
	Primary   *window `json:"primary_window"`
	Secondary *window `json:"secondary_window"`
}

func (windows rateLimitWindows) weekly() *window {
	const weekSeconds = 7 * 24 * 60 * 60
	for _, candidate := range []*window{windows.Secondary, windows.Primary} {
		if candidate != nil && candidate.LimitWindowSeconds == weekSeconds {
			return candidate
		}
	}
	// Older responses did not always include the duration, but consistently
	// exposed their weekly quota as the secondary window.
	if windows.Secondary != nil && windows.Secondary.LimitWindowSeconds == 0 {
		return windows.Secondary
	}
	return nil
}

func decodeClaudeWindow(raw json.RawMessage) (Snapshot, error) {
	var value struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return Snapshot{}, err
	}
	if value.Utilization == nil {
		return Snapshot{}, fmt.Errorf("utilization is missing")
	}
	used := *value.Utilization
	if used <= 1 {
		used *= 100
	}
	if !validPercent(used) {
		return Snapshot{}, fmt.Errorf("utilization %.2f is outside 0-100%%", used)
	}
	reset, _ := time.Parse(time.RFC3339, value.ResetsAt)
	return Snapshot{UsedPercent: used, ResetAt: reset}, nil
}

func readClaudeCredentials(ctx context.Context, path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil || runtime.GOOS != "darwin" || !os.IsNotExist(err) {
		return raw, err
	}
	cmd := exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
	out, keychainErr := cmd.Output()
	if keychainErr != nil {
		return nil, err
	}
	return out, nil
}

func validPercent(value float64) bool { return value >= 0 && value <= 100 }

func (c Client) doJSON(req *http.Request, target any) error {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("usage API returned HTTP %d", resp.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("decode usage response: %w", err)
	}
	return nil
}

func configRoot(env map[string]string, variable, fallback string) string {
	if root := strings.TrimSpace(env[variable]); root != "" {
		return root
	}
	home := strings.TrimSpace(env["HOME"])
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, fallback)
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	return ""
}

func selectedModel(args []string) string {
	for i, arg := range args {
		if (arg == "--model" || arg == "-m") && i+1 < len(args) {
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, "--model="); ok {
			return value
		}
	}
	return ""
}

func unixTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(value, 0)
}

func Percent(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64) + "%"
}
