//go:build !windows

package userterm

import (
	"os"
	"path/filepath"
)

// DefaultShell returns $SHELL when it is an absolute path to an executable
// file, else /bin/sh.
func DefaultShell() (string, []string) {
	if sh := os.Getenv("SHELL"); filepath.IsAbs(sh) {
		if st, err := os.Stat(sh); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			return sh, nil
		}
	}
	return "/bin/sh", nil
}
