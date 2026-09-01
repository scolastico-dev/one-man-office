package bundledplugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureNudgeInstallsOnceAndPreservesEdits(t *testing.T) {
	office := t.TempDir()
	installed, err := EnsureNudge(office)
	if err != nil || !installed {
		t.Fatalf("first ensure installed=%v err=%v", installed, err)
	}
	script := filepath.Join(office, ".omo", "plugins", "nudge", "nudge.lua")
	if _, err := os.Stat(filepath.Join(office, ".omo", "plugins", "nudge", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("-- customized"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed, err = EnsureNudge(office)
	if err != nil || installed {
		t.Fatalf("second ensure installed=%v err=%v", installed, err)
	}
	raw, err := os.ReadFile(script)
	if err != nil || string(raw) != "-- customized" {
		t.Fatalf("existing plugin was overwritten: %q err=%v", raw, err)
	}
}
