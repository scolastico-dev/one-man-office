package config

import (
	"os"
	"path/filepath"
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
