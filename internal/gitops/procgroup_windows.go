//go:build windows

package gitops

import (
	"os"
	"os/exec"
	"time"
)

// killWithChildren is handled by processtree.Run/CombinedOutput on Windows:
// the root is in a kill-on-close job before it runs, including git helpers.
// Windows has no console SIGTERM grace period, as before.
func killWithChildren(cmd *exec.Cmd, grace time.Duration) (afterWait func()) {
	return func() {}
}

// openNoFollow opens path for reading. The caller has checked with Lstat
// that it is a regular file and compares the opened file with that.
func openNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}
