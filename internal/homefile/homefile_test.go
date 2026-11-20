package homefile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A settings save or a plugin tree swap must not depend on whether another
// goroutine has finished reading the earlier file. Keep the reader open across
// each filesystem operation instead of trying to race it with a short sleep.
func TestOpenAllowsReplacementDuringRead(t *testing.T) {
	for _, depth := range []int{1, 8} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			root := t.TempDir()
			for range depth {
				root = filepath.Join(root, strings.Repeat("folder", 8))
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "plugin")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "plugin.json")
			if err := os.WriteFile(path, []byte("earlier"), 0o600); err != nil {
				t.Fatal(err)
			}
			reader, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			moved := filepath.Join(root, "earlier")
			if err := os.Rename(dir, moved); err != nil {
				t.Fatalf("move the tree with an open reader: %v", err)
			}
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(moved, "plugin.json")); err != nil {
				t.Fatalf("delete the earlier file with an open reader: %v", err)
			}
			if got, err := io.ReadAll(reader); err != nil || string(got) != "earlier" {
				t.Fatalf("open reader = %q, %v", got, err)
			}
			// Windows may keep a deleted file's directory entry until the last
			// handle closes, so finish the reader before removing its directory.
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(moved); err != nil {
				t.Fatal(err)
			}
			if got, err := Read(path, 32); err != nil || string(got) != "replacement" {
				t.Fatalf("new reader = %q, %v", got, err)
			}
		})
	}
}

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
