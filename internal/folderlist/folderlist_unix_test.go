//go:build unix

package folderlist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestListLinksNotFollowed(t *testing.T) {
	root := t.TempDir()
	repo := mkdir(t, root, "repo", ".git")
	_ = repo
	other := t.TempDir()
	mkdir(t, other, ".git")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink(other, filepath.Join(root, "linked")))
	must(os.Symlink(filepath.Join(root, "file.txt"), filepath.Join(root, "filelink")))
	must(os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, "broken")))
	// a .git that is a link to a folder is not a repository mark
	mkdir(t, root, "faked")
	must(os.Symlink(filepath.Join(root, "repo", ".git"), filepath.Join(root, "faked", ".git")))

	l, err := List(context.Background(), root, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(names(l), []string{"faked", "linked", "repo"}) {
		t.Fatalf("names = %v", names(l))
	}
	by := map[string]Entry{}
	for _, e := range l.Folders {
		by[e.Name] = e
	}
	if !by["linked"].Link || by["linked"].Repository {
		t.Errorf("linked = %+v", by["linked"])
	}
	if by["faked"].Repository || by["faked"].Link {
		t.Errorf("faked = %+v", by["faked"])
	}
	if by["repo"].Link || !by["repo"].Repository {
		t.Errorf("repo = %+v", by["repo"])
	}
}

func TestListFolderItselfMayBeALink(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "real", "inner")
	must := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "via"))
	if must != nil {
		t.Fatal(must)
	}
	l, err := List(context.Background(), filepath.Join(root, "via"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != filepath.Join(root, "via") || !equal(names(l), []string{"inner"}) {
		t.Errorf("listing = %+v", l)
	}
}

func TestListPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	root := t.TempDir()
	locked := mkdir(t, root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := List(context.Background(), locked, 10); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v", err)
	}
	// the locked folder still shows in its parent
	l, err := List(context.Background(), root, 10)
	if err != nil || !equal(names(l), []string{"locked"}) {
		t.Fatalf("%+v %v", l, err)
	}
}
