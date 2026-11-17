//go:build !windows

package transport

import (
	"fmt"
	"os"
	"path/filepath"
)

const socketName = "omo.sock"

// Endpoint creates a short Unix-socket location and exposes runDir/omo.sock as
// a symlink to it. Clients dial the short target, which avoids sun_path's
// byte limit even when the home lives deep in the filesystem.
func Endpoint(runDir string) (actual, display string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "omo-sock-")
	if err != nil {
		return "", "", nil, err
	}
	actual = filepath.Join(tmp, socketName)
	display = filepath.Join(runDir, socketName)
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

// ClientEndpoint resolves the socket link in runDir to the short path the
// server listens on.
func ClientEndpoint(runDir string) (string, error) {
	return os.Readlink(filepath.Join(runDir, socketName))
}
