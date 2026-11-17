//go:build windows

package transport

import (
	"crypto/sha256"
	"fmt"
)

func pipeName(runDir string) string {
	sum := sha256.Sum256([]byte(runDir))
	return fmt.Sprintf(`\\.\pipe\omo-%x`, sum[:10])
}

// Endpoint returns the named pipe for runDir; there is no filesystem socket or
// path limit on Windows.
func Endpoint(runDir string) (actual, display string, cleanup func(), err error) {
	actual = pipeName(runDir)
	return actual, actual, func() {}, nil
}

// ClientEndpoint returns the named pipe for runDir.
func ClientEndpoint(runDir string) (string, error) {
	return pipeName(runDir), nil
}
