//go:build !windows

package company

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestValidateSetupTreeRejectsOfficeSymlinksBeforeSetupWrites(t *testing.T) {
	for _, link := range []string{".omo", ".omo/messages"} {
		t.Run(link, func(t *testing.T) {
			dir := projectHome(t)
			destination := filepath.Join(dir, "clone")
			outside := filepath.Join(dir, "outside")
			if err := os.Mkdir(outside, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(destination, 0700); err != nil {
				t.Fatal(err)
			}
			linkPath := filepath.Join(destination, link)
			if err := os.MkdirAll(filepath.Dir(linkPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, linkPath); err != nil {
				t.Fatal(err)
			}
			if err := ValidateSetupTree(destination); err == nil {
				t.Fatal("office symlink accepted")
			}
		})
	}
}

func TestValidateSetupTreeRejectsSpecialFiles(t *testing.T) {
	dir := projectHome(t)
	officeDir := filepath.Join(dir, "clone")
	if err := os.MkdirAll(filepath.Join(officeDir, ".omo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(officeDir, ".omo", "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSetupTree(officeDir); err == nil {
		t.Fatal("special file accepted")
	}
}
