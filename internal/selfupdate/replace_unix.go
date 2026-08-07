//go:build !windows

package selfupdate

import "os"

func replaceExecutable(staged, target string) error {
	return os.Rename(staged, target)
}
