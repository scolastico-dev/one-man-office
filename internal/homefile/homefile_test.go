package homefile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.yaml")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(path, 3); err != nil || string(got) != "abc" {
		t.Fatalf("Read = %q, %v", got, err)
	}
	if _, err := Read(path, 2); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := Read(filepath.Join(dir, "missing"), 10); !os.IsNotExist(err) {
		t.Fatalf("missing: %v", err)
	}
}

func TestReadRefusesFolder(t *testing.T) {
	dir := t.TempDir()
	if _, err := Read(dir, 10); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("folder: %v", err)
	}
	if _, err := Open(dir); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("folder: %v", err)
	}
}

func TestRefusalIsFixed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret-name")
	for _, c := range []struct {
		err  error
		want string
	}{
		{&os.PathError{Op: "open", Path: path, Err: ErrLink}, "the file is a link; omo reads only regular files here"},
		{&os.PathError{Op: "open", Path: path, Err: ErrNotRegular}, "the file is not a regular file (a folder, a named pipe or a device); omo reads only regular files here"},
		{&os.PathError{Op: "read", Path: path, Err: ErrTooLarge}, "the file is larger than 1 MiB"},
	} {
		got, ok := Refusal(c.err, MaxSettingsFile)
		if !ok || got != c.want || strings.Contains(got, "secret") {
			t.Errorf("Refusal(%v) = %q, %v", c.err, got, ok)
		}
	}
	if _, ok := Refusal(os.ErrPermission, 1); ok {
		t.Error("another error is a refusal")
	}
	if Size(256<<10) != "256 KiB" || Size(MaxPinsFile) != "16 MiB" || Size(100) != "100 bytes" {
		t.Error("Size")
	}
}
