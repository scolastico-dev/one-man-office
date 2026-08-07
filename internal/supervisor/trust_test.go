package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
)

// claudeStub is a fake CLI whose *name* contains "claude", which is how omo
// decides a profile needs the trust dance.
func claudeStub(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "claude-stub")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func trustedDirs(t *testing.T, home string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("trust config is not valid JSON: %v", err)
	}
	p, _ := m["projects"].(map[string]any)
	return p
}

func TestSpawnTrustsWorkdirForClaudeProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // never touch the real ~/.claude.json

	o := newOffice(t, map[string]string{})
	p := config.Profile{Cmd: claudeStub(t, home)}
	o.Sup.Cfg.Models["freelancer"] = p

	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "g"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if entry, ok := trustedDirs(t, home)[o.Dir].(map[string]any); ok && entry["hasTrustDialogAccepted"] == true {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("workdir %s was not pre-trusted: %v", o.Dir, trustedDirs(t, home))
}

func TestTrustCanBeDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	o := newOffice(t, map[string]string{})
	off := false
	o.Sup.Cfg.TrustWorkdirs = &off
	o.Sup.Cfg.Models["freelancer"] = config.Profile{Cmd: claudeStub(t, home)}

	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "g"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if len(trustedDirs(t, home)) != 0 {
		t.Fatal("trust_workdirs: false must leave the claude config alone")
	}
}

// Non-claude runners must not have their config touched at all.
func TestNoTrustWriteForNonClaudeProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	o := newOffice(t, map[string]string{"freelancer": "ready\nsleep|5s\n"})
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "g"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("omo created a claude config for a runner that is not claude")
	}
}
