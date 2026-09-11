//go:build !windows

package company

import "os"

func secureHTTPDirectory(path string) error {
	return os.Chmod(path, 0o700)
}
