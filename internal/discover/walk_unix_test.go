//go:build unix

package discover

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWalkDoesNotFollowLinks(t *testing.T) {
	root := tempRoot(t)
	outside := tempRoot(t)
	mkRepo(t, outside, "secret")
	mkRepo(t, root, "kept")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	// A link to a repository and a loop back to the root.
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "repo-link")); err != nil {
		t.Fatal(err)
	}
	mkDir(t, root, "loop")
	if err := os.Symlink(root, filepath.Join(root, "loop", "back")); err != nil {
		t.Fatal(err)
	}
	// A dangling link and a link to a file are not counted.
	if err := os.Symlink(filepath.Join(root, "absent"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "f"), filepath.Join(root, "file-link")); err != nil {
		t.Fatal(err)
	}
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 8}, Options{})
	if got, want := rels(res), []string{"kept"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("repos = %v, want %v", got, want)
	}
	if res.Links != 3 {
		t.Fatalf("links = %d, want 3", res.Links)
	}
	if res.State != StateOK {
		t.Fatalf("state = %q %q", res.State, res.Message)
	}
}

func TestWalkLinkedGitIsNoRepo(t *testing.T) {
	root := tempRoot(t)
	other := tempRoot(t)
	mkRepo(t, other, "r")
	mkDir(t, root, "fake")
	if err := os.Symlink(filepath.Join(other, "r", ".git"), filepath.Join(root, "fake", ".git")); err != nil {
		t.Fatal(err)
	}
	mkRepo(t, root, "fake/inner")
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 4}, Options{})
	if len(res.Repos) != 0 {
		t.Fatalf("repos = %v", rels(res))
	}
}

func TestWalkResolvesTheRootsOwnLinks(t *testing.T) {
	real := tempRoot(t)
	mkRepo(t, real, "a")
	home := filepath.Join(real, "home")
	mkRepo(t, home, "inside")
	link := filepath.Join(tempRoot(t), "ws")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// The skip folder is named through the link, the walk sees real paths.
	res := Walk(context.Background(), Root{Path: link, MaxDepth: 4}, Options{Skip: []string{filepath.Join(link, "home")}})
	if want := []Repo{{Path: filepath.Join(real, "a"), Rel: "a"}}; !reflect.DeepEqual(res.Repos, want) {
		t.Fatalf("repos = %+v, want %+v", res.Repos, want)
	}
}

func TestWalkUnreadableFolderIsPassedOver(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	root := tempRoot(t)
	mkRepo(t, root, "locked/r")
	mkRepo(t, root, "open")
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 4}, Options{ReadTimeout: time.Second})
	if res.State != StateOK || !reflect.DeepEqual(rels(res), []string{"open"}) || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v", res)
	}
}
