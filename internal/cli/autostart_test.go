package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAutostartRegisterAndUnregister(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the login entry location is only isolated on Linux")
	}
	home := isolate(t)
	out := mustExecute(t, "autostart", "register")
	if !strings.Contains(out, "autostart registered") {
		t.Fatalf("register output: %q", out)
	}
	if _, err := os.Stat(filepath.Join(home, "run", "autostart.json")); err != nil {
		t.Fatal(err)
	}
	out = mustExecute(t, "autostart", "unregister")
	if !strings.Contains(out, "autostart unregistered") {
		t.Fatalf("unregister output: %q", out)
	}
	if _, err := os.Stat(filepath.Join(home, "run", "autostart.json")); !os.IsNotExist(err) {
		t.Fatalf("autostart.json remains: %v", err)
	}
}

func TestAutostartRunRejectsMissingSettings(t *testing.T) {
	isolate(t)
	if _, err := execute(t.Context(), "autostart-run", filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing settings accepted")
	}
}
