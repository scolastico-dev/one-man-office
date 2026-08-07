package config

import (
	"os"
	"path/filepath"
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
	if time.Duration(cfg.SmokeAlarm.Interval) != 5*time.Minute || cfg.SmokeAlarm.TailLines != 120 {
		t.Fatalf("smokealarm defaults wrong: %+v", cfg.SmokeAlarm)
	}
	if !cfg.SmokeAlarm.Enabled || cfg.SmokeAlarm.Mode != "all" || cfg.SmokeAlarm.HistoryRuns != 3 || !cfg.SmokeAlarm.IncludeEvents || !cfg.SmokeAlarm.IncludePMChatter {
		t.Fatalf("extended smokealarm defaults wrong: %+v", cfg.SmokeAlarm)
	}
	if !cfg.Startup.CheckSelfUpdate || !cfg.Startup.CheckTemplates || time.Duration(cfg.Startup.CheckTimeout) != 5*time.Second {
		t.Fatalf("startup defaults wrong: %+v", cfg.Startup)
	}
	if time.Duration(cfg.Agents.ReadyTimeout) != 2*time.Minute || cfg.Agents.MaxSpawnRetries != 2 || cfg.Agents.MaxJobRetries != 3 {
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
	if cfg.Logs.Keep != 50 || cfg.Reviews.EscalateAfter != 2 {
		t.Fatalf("retention/review defaults wrong: logs=%+v reviews=%+v", cfg.Logs, cfg.Reviews)
	}
	if time.Duration(cfg.Notifications.InputDebounce) != 30*time.Second || time.Duration(cfg.Notifications.RepeatInterval) != 3*time.Minute {
		t.Fatalf("notification defaults wrong: %+v", cfg.Notifications)
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
		"check_self_update: true", "check_templates: false", "check_timeout: 5s",
		"ready_timeout: 2m", "history_runs: 7", "include_events: true",
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

func TestLoadRejectsInvalidExtendedSettings(t *testing.T) {
	for _, addition := range []string{
		"smokealarm:\n  mode: together\n",
		"agents:\n  ready_timeout: 0s\n",
		"limits:\n  max_developers: -1\n",
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
	if !cfg.Startup.CheckSelfUpdate || !cfg.Startup.CheckTemplates {
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
