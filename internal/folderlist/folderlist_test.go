package folderlist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func names(l Listing) []string {
	var out []string
	for _, e := range l.Folders {
		out = append(out, e.Name)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListFoldersOnly(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "beta")
	mkdir(t, root, "Alpha")
	mkdir(t, root, "repo", ".git")
	mkdir(t, root, ".hidden")
	mkdir(t, root, "gitfile")
	if err := os.WriteFile(filepath.Join(root, "gitfile", ".git"), []byte("gitdir: x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := List(context.Background(), root, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Alpha", "beta", "gitfile", "repo", ".hidden"}
	if !equal(names(l), want) {
		t.Fatalf("names = %v, want %v", names(l), want)
	}
	byName := map[string]Entry{}
	for _, e := range l.Folders {
		byName[e.Name] = e
		if e.Path != filepath.Join(root, e.Name) {
			t.Errorf("path of %s = %s", e.Name, e.Path)
		}
	}
	if !byName["repo"].Repository || byName["gitfile"].Repository || byName["beta"].Repository {
		t.Errorf("repository marks wrong: %+v", byName)
	}
	if !byName[".hidden"].Hidden || byName["beta"].Hidden {
		t.Errorf("hidden marks wrong: %+v", byName)
	}
	if l.Path != root || l.Parent != filepath.Dir(root) || l.More {
		t.Errorf("listing = %+v", l)
	}
}

func TestListHiddenCharactersInNames(t *testing.T) {
	root := t.TempDir()
	odd := "a‮b\x1b"
	if err := os.Mkdir(filepath.Join(root, odd), 0o755); err != nil {
		t.Skip("file system refuses the name")
	}
	l, err := List(context.Background(), root, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Folders) != 1 || l.Folders[0].Name != `a\u{202E}b\u{1B}` {
		t.Fatalf("folders = %+v", l.Folders)
	}
	if l.Folders[0].Path != filepath.Join(root, odd) {
		t.Errorf("path must stay exact, got %q", l.Folders[0].Path)
	}
}

func TestListMax(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		mkdir(t, root, "d"+strconv.Itoa(i))
	}
	l, err := List(context.Background(), root, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Folders) != 3 || !l.More {
		t.Fatalf("got %d folders, more=%v", len(l.Folders), l.More)
	}
	l, err = List(context.Background(), root, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Folders) != 5 || l.More {
		t.Fatalf("got %d folders, more=%v", len(l.Folders), l.More)
	}
}

func TestListMaxDefaultsAndCaps(t *testing.T) {
	if clampMax(0) != MaxEntries || clampMax(-1) != MaxEntries || clampMax(MaxEntries+1) != MaxEntries || clampMax(7) != 7 {
		t.Fatal("clampMax")
	}
}

func TestListErrors(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := List(ctx, "relative/path", 10); !errors.Is(err, ErrNotAbsolute) {
		t.Errorf("relative: %v", err)
	}
	if _, err := List(ctx, "../x", 10); !errors.Is(err, ErrNotAbsolute) {
		t.Errorf("dotdot: %v", err)
	}
	if _, err := List(ctx, filepath.Join(root, "missing"), 10); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	if _, err := List(ctx, file, 10); !errors.Is(err, ErrNotFolder) {
		t.Errorf("file: %v", err)
	}
}

func TestListCleansPath(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "a", "b")
	l, err := List(context.Background(), filepath.Join(root, "a", "b", "..", "b")+string(filepath.Separator), 10)
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != filepath.Join(root, "a", "b") {
		t.Errorf("path = %s", l.Path)
	}
}

func TestListHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	mkdir(t, home, "docs")
	for _, in := range []string{"", "~"} {
		l, err := List(context.Background(), in, 10)
		if err != nil {
			t.Fatal(err)
		}
		if l.Path != home || l.Home != home || !equal(names(l), []string{"docs"}) {
			t.Errorf("%q: %+v", in, l)
		}
	}
	l, err := List(context.Background(), "~/docs", 10)
	if err != nil || l.Path != filepath.Join(home, "docs") {
		t.Errorf("~/docs: %+v %v", l, err)
	}
}

func TestListTopHasNoParent(t *testing.T) {
	top := filepath.Dir(t.TempDir())
	for filepath.Dir(top) != top {
		top = filepath.Dir(top)
	}
	l, err := List(context.Background(), top, 5)
	if err != nil {
		t.Skip("cannot list the top folder")
	}
	if l.Parent != "" {
		t.Errorf("parent = %q", l.Parent)
	}
}

func TestListCancelled(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := List(ctx, root, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
