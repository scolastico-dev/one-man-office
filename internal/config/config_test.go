package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const validYAML = `
repos:
  api: /tmp/repo-api
models:
  fable:
    cmd: claude
    args: ["--model", "fable"]
    selectable: false
  sonnet:
    cmd: claude
    args: ["--model", "sonnet"]
roles:
  ceo: fable
  product_manager: sonnet
  developer: sonnet
  reviewer: sonnet
  freelancer: sonnet
  smokealarm: sonnet
  firefighter: sonnet
`

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "omo.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValidAppliesDefaults(t *testing.T) {
	cfg, err := Load(write(t, validYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Limits.MaxDevelopers != 4 || cfg.Limits.MaxFreelancers != 2 {
		t.Fatalf("limit defaults wrong: %+v", cfg.Limits)
	}
	if time.Duration(cfg.SmokeAlarm.Interval) != 5*time.Minute || time.Duration(cfg.SmokeAlarm.Timeout) != 2*time.Minute || cfg.SmokeAlarm.TailLines != 120 {
		t.Fatalf("smokealarm defaults wrong: %+v", cfg.SmokeAlarm)
	}
	if !cfg.SmokeAlarm.Enabled || cfg.SmokeAlarm.Mode != "all" || cfg.SmokeAlarm.HistoryRuns != 3 || !cfg.SmokeAlarm.IncludeEvents || !cfg.SmokeAlarm.IncludePMChatter {
		t.Fatalf("extended smokealarm defaults wrong: %+v", cfg.SmokeAlarm)
	}
	if !cfg.Startup.CheckSelfUpdate || !cfg.Startup.CheckTemplates || !cfg.Startup.CheckSuperpowers || time.Duration(cfg.Startup.CheckTimeout) != 5*time.Second {
		t.Fatalf("startup defaults wrong: %+v", cfg.Startup)
	}
	if time.Duration(cfg.Agents.ReadyTimeout) != 2*time.Minute || cfg.Agents.MaxSpawnRetries != 2 || cfg.Agents.MaxJobRetries != 3 || !cfg.Agents.LowerPriority || cfg.Agents.NiceIncrement != 10 {
		t.Fatalf("agent defaults wrong: %+v", cfg.Agents)
	}
	if cfg.CEO.MaxRestarts != 3 || time.Duration(cfg.CEO.RestartWindow) != 30*time.Second {
		t.Fatalf("CEO defaults wrong: %+v", cfg.CEO)
	}
	if cfg.Models["fable"].IsSelectable() {
		t.Fatal("fable must not be selectable")
	}
	if !cfg.Models["sonnet"].IsSelectable() {
		t.Fatal("sonnet must default to selectable")
	}
	if !cfg.Models["sonnet"].ShouldInjectPrompt() || cfg.Models["sonnet"].InitialPromptRetryCount() != 3 || cfg.Models["sonnet"].InitialPromptRetryWait() != 30*time.Second {
		t.Fatalf("prompt defaults wrong: %+v", cfg.Models["sonnet"])
	}
	if cfg.Logs.Keep != 50 || cfg.Reviews.EscalateAfter != 2 {
		t.Fatalf("retention/review defaults wrong: logs=%+v reviews=%+v", cfg.Logs, cfg.Reviews)
	}
	if time.Duration(cfg.Notifications.InputDebounce) != 30*time.Second || time.Duration(cfg.Notifications.RepeatInterval) != 3*time.Minute || time.Duration(cfg.Notifications.StatusStaleAfter) != 15*time.Minute {
		t.Fatalf("notification defaults wrong: %+v", cfg.Notifications)
	}
	if cfg.Cleanup.Enabled() || time.Duration(cfg.Cleanup.Interval) != time.Hour {
		t.Fatalf("cleanup should default fully off with an hourly interval: %+v", cfg.Cleanup)
	}
	if !cfg.Usage.Enabled || cfg.Usage.WeeklyLimitPercent != 90 {
		t.Fatalf("usage defaults = %+v, want enabled with weekly limit 90", cfg.Usage)
	}
}

func TestUsageCanBeDisabled(t *testing.T) {
	raw := validYAML + "\nusage:\n  enabled: false\n  weekly_limit_percent: 90\n"
	cfg, err := Load(write(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Usage.Enabled {
		t.Fatalf("usage = %+v, want disabled", cfg.Usage)
	}
}

func TestSmartAssignmentRequiresMeteredProfiles(t *testing.T) {
	raw := strings.Replace(validYAML, "  developer: sonnet", "  developer:\n    models: [sonnet, fable]\n    assignment: smart", 1)
	if _, err := Load(write(t, raw)); err != nil {
		t.Fatalf("Claude smart assignment rejected: %v", err)
	}
	raw = strings.Replace(raw, "cmd: claude", "cmd: custom-runner", 1)
	if _, err := Load(write(t, raw)); err == nil || !strings.Contains(err.Error(), "must use claude or codex") {
		t.Fatalf("unsupported smart profile error = %v", err)
	}
}

func TestWeeklyUsageLimitValidation(t *testing.T) {
	raw := validYAML + "\nusage:\n  weekly_limit_percent: 0\n"
	if _, err := Load(write(t, raw)); err == nil || !strings.Contains(err.Error(), "weekly_limit_percent") {
		t.Fatalf("invalid usage limit error = %v", err)
	}
}

func TestLoadReadOnlyDoesNotWriteMissingDefaults(t *testing.T) {
	path := write(t, validYAML)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limits.MaxDevelopers != 4 {
		t.Fatalf("defaults were not applied in memory: %+v", cfg.Limits)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("read-only config load rewrote omo.yaml")
	}
}

func TestLoadModelPromptDeliverySettings(t *testing.T) {
	raw := strings.Replace(validYAML, "    args: [\"--model\", \"sonnet\"]", `    args: ["--prompt", "%prompt%"]
    prompt_delay: 750ms
    inject_prompt: false
    prompt_retry_count: 0
    prompt_retry_wait: 5s`, 1)
	cfg, err := Load(write(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	profile := cfg.Models["sonnet"]
	if profile.ShouldInjectPrompt() || profile.InitialPromptDelay(time.Second) != 750*time.Millisecond || profile.InitialPromptRetryCount() != 0 || profile.InitialPromptRetryWait() != 5*time.Second {
		t.Fatalf("prompt settings = %+v", profile)
	}
}

func TestLoadRejectsInvalidModelPromptDeliverySettings(t *testing.T) {
	for _, setting := range []string{
		"prompt_delay: -1s",
		"prompt_retry_count: -1",
		"prompt_retry_count: 0\n    prompt_retry_wait: -1s",
		"prompt_retry_count: 2\n    prompt_retry_wait: 0s",
	} {
		raw := strings.Replace(validYAML, "    args: [\"--model\", \"sonnet\"]", "    args: [\"--model\", \"sonnet\"]\n    "+setting, 1)
		if _, err := Load(write(t, raw)); err == nil {
			t.Errorf("expected error for %s", setting)
		}
	}
}

func TestLoadWritesBackOnlyMissingDefaultKeys(t *testing.T) {
	path := write(t, validYAML+`startup:
  check_templates: false
smokealarm:
  history_runs: 7
`)
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"check_self_update: true", "check_templates: false", "check_superpowers: true", "check_timeout: 5s",
		"ready_timeout: 2m", "lower_priority: true", "nice_increment: 10", "history_runs: 7", "timeout: 2m", "include_events: true", "status_stale_after: 15m",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("written config missing %q:\n%s", want, text)
		}
	}
	before := text
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != before {
		t.Fatal("second load rewrote a config that already had every default key")
	}
}

func TestLoadCanDisableLowerAgentPriority(t *testing.T) {
	cfg, err := Load(write(t, validYAML+"agents:\n  lower_priority: false\n  nice_increment: 7\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents.LowerPriority {
		t.Fatal("agents.lower_priority=false was not preserved")
	}
	if cfg.Agents.NiceIncrement != 7 {
		t.Fatalf("agents.nice_increment = %d, want 7", cfg.Agents.NiceIncrement)
	}
}

func TestLoadRejectsInvalidExtendedSettings(t *testing.T) {
	for _, addition := range []string{
		"smokealarm:\n  mode: together\n",
		"smokealarm:\n  timeout: 0s\n",
		"agents:\n  ready_timeout: 0s\n",
		"agents:\n  nice_increment: 0\n",
		"agents:\n  nice_increment: 20\n",
		"limits:\n  max_developers: -1\n",
		"cleanup:\n  read_messages_after: -1s\n",
		"cleanup:\n  interval: 0s\n  terminal_jobs_after: 1h\n",
		"notifications:\n  status_stale_after: -1s\n",
	} {
		if _, err := Load(write(t, validYAML+addition)); err == nil {
			t.Fatalf("expected validation error for:\n%s", addition)
		}
	}
}

func TestLoadReplacesNullDefaultSection(t *testing.T) {
	path := write(t, validYAML+"startup: null\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Startup.CheckSelfUpdate || !cfg.Startup.CheckTemplates || !cfg.Startup.CheckSuperpowers {
		t.Fatalf("null startup did not retain defaults: %+v", cfg.Startup)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "check_self_update: true") {
		t.Fatalf("null startup was not written back as a mapping:\n%s", raw)
	}
}

func TestLoadRejectsMissingRole(t *testing.T) {
	bad := validYAML[:len(validYAML)-len("  firefighter: sonnet\n")]
	if _, err := Load(write(t, bad)); err == nil {
		t.Fatal("expected error for missing firefighter role")
	}
}

func TestLoadRejectsUnknownProfileRef(t *testing.T) {
	if _, err := Load(write(t, validYAML+"  extra: nope\n")); err == nil {
		t.Fatal("expected error for unknown role key")
	}
}

func TestLoadRoleModelSets(t *testing.T) {
	raw := strings.Replace(validYAML, "  developer: sonnet", `  developer:
    models: [sonnet, fable]
    assignment: failover`, 1)
	raw = strings.Replace(raw, "  reviewer: sonnet", "  reviewer: [sonnet, fable]", 1)
	cfg, err := Load(write(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Roles["developer"]; !slices.Equal(got.Models, []string{"sonnet", "fable"}) || got.Assignment != AssignmentFailover {
		t.Fatalf("developer role = %+v", got)
	}
	if got := cfg.Roles["reviewer"]; !slices.Equal(got.Models, []string{"sonnet", "fable"}) || got.Assignment != AssignmentRoundRobin {
		t.Fatalf("reviewer role = %+v", got)
	}
	if got := cfg.Roles["ceo"]; !slices.Equal(got.Models, []string{"fable"}) || got.Assignment != AssignmentRoundRobin {
		t.Fatalf("legacy scalar role = %+v", got)
	}
}

func TestLoadRejectsInvalidRoleModelSets(t *testing.T) {
	tests := []string{
		"  developer: []",
		"  developer: [sonnet, ghost]",
		"  developer: [sonnet, sonnet]",
		"  developer: {models: [sonnet], assignment: nearest}",
		"  developer: {models: [sonnet], strategy: random}",
	}
	for _, replacement := range tests {
		raw := strings.Replace(validYAML, "  developer: sonnet", replacement, 1)
		if _, err := Load(write(t, raw)); err == nil {
			t.Errorf("expected error for %s", replacement)
		}
	}
}

func TestProfileForJob(t *testing.T) {
	cfg, _ := Load(write(t, validYAML))
	key, _, err := cfg.ProfileForJob("developer", "")
	if err != nil || key != "sonnet" {
		t.Fatalf("default: key=%q err=%v", key, err)
	}
	if _, _, err := cfg.ProfileForJob("developer", "fable"); err == nil {
		t.Fatal("non-selectable override must be rejected")
	}
	if _, _, err := cfg.ProfileForJob("developer", "ghost"); err == nil {
		t.Fatal("unknown override must be rejected")
	}
}
