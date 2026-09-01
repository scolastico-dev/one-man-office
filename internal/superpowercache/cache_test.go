package superpowercache

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestEnsureInstallsThenUpdatesBesideExecutable(t *testing.T) {
	root := t.TempDir()
	oldExecutable, oldRunGit := Executable, RunGit
	t.Cleanup(func() { Executable, RunGit = oldExecutable, oldRunGit })
	Executable = func() (string, error) { return filepath.Join(root, "bin", "omo"), nil }
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	RunGit = func(_ context.Context, args ...string) error {
		calls = append(calls, slices.Clone(args))
		if len(args) > 0 && args[0] == "clone" {
			destination := args[len(args)-1]
			return os.MkdirAll(filepath.Join(destination, ".git"), 0o755)
		}
		return nil
	}
	want := filepath.Join(root, "bin", "omo-superpowers")
	if got, err := Ensure(context.Background()); err != nil || got != want {
		t.Fatalf("install = %q, %v", got, err)
	}
	if got, err := Ensure(context.Background()); err != nil || got != want {
		t.Fatalf("update = %q, %v", got, err)
	}
	if len(calls) != 2 || calls[0][0] != "clone" || calls[1][0] != "-C" || calls[1][2] != "pull" {
		t.Fatalf("git calls = %#v", calls)
	}
}
