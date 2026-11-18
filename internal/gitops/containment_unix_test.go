//go:build !windows

package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainmentIgnoresMergePoliciesGit238(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	program := filepath.Join(bin, "git")
	write(t, program, "#!/bin/sh\nfor arg in \"$@\"; do\ncase \"$arg\" in --merge-base=*) echo 'error: unknown option merge-base' >&2; exit 129;; esac\ndone\nexec '"+strings.ReplaceAll(realGit, "'", "'\\''")+"' \"$@\"\n")
	if err := os.Chmod(program, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	testContainmentMergePolicies(t)
}
