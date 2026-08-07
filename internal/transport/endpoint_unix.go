//go:build !windows

package transport

import (
	"fmt"
	"os"
	"path/filepath"
)

// Endpoint creates a short Unix-socket location and exposes the familiar
// .omo/omo.sock path as a symlink. Agents receive Actual, avoiding sun_path's
// byte limit even when the office lives deep in the filesystem.
func Endpoint(officeDir string) (actual, display string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "omo-sock-")
	if err != nil {
		return "", "", nil, err
	}
	actual = filepath.Join(tmp, "omo.sock")
	display = filepath.Join(officeDir, ".omo", "omo.sock")
	if err := os.Remove(display); err != nil && !os.IsNotExist(err) {
		os.RemoveAll(tmp)
		return "", "", nil, fmt.Errorf("remove stale socket link: %w", err)
	}
	if err := os.Symlink(actual, display); err != nil {
		os.RemoveAll(tmp)
		return "", "", nil, fmt.Errorf("create socket link: %w", err)
	}
	cleanup = func() {
		os.Remove(display)
		os.RemoveAll(tmp)
	}
	return actual, display, cleanup, nil
}
