//go:build !windows

package company

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCloneRejectsOfficeSymlinksBeforeSetupWrites(t *testing.T) {
	for _, link := range []string{".omo", ".omo/messages"} {
		t.Run(link, func(t *testing.T) {
			dir := projectHome(t)
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "absent-gitconfig"))
			source := filepath.Join(dir, "source")
			outside := filepath.Join(dir, "outside")
			if err := os.Mkdir(outside, 0700); err != nil {
				t.Fatal(err)
			}
			run := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git: %s: %v", out, err)
				}
			}
			run("init", "-q", source)
			linkPath := filepath.Join(source, link)
			if err := os.MkdirAll(filepath.Dir(linkPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, linkPath); err != nil {
				t.Fatal(err)
			}
			run("-C", source, "add", ".omo")
			run("-C", source, "commit", "-qm", "fixture")
			destination := filepath.Join(dir, "clone")
			if _, err := CreateProject(context.Background(), destination, source); err == nil {
				t.Fatal("clone with office symlink was set up and trusted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("setup wrote outside destination: %v: %v", entries, err)
			}
			if _, err := TrustedProject(destination); err == nil {
				t.Fatal("rejected clone was trusted")
			}
		})
	}
}
