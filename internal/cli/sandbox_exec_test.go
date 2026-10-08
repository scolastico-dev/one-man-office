package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxExecCommandIsHiddenAndFailsClosed(t *testing.T) {
	root := Root("test")
	command, _, err := root.Find([]string{"__sandbox-exec"})
	if err != nil || command == root || !command.Hidden {
		t.Fatalf("hidden sandbox command missing: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "marker")
	root.SetArgs([]string{"__sandbox-exec", "--policy", filepath.Join(t.TempDir(), "missing.json"), "--", "/bin/sh", "-c", "touch " + marker})
	if err := root.Execute(); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command ran without policy: %v", err)
	}
}
