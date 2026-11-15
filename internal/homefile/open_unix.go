//go:build unix

package homefile

import (
	"errors"
	"os"
	"syscall"
)

// openNoFollow opens path for reading without following a link at its last
// element and without waiting for a writer of a named pipe.
func openNoFollow(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil && errors.Is(err, syscall.ELOOP) {
		if info, lerr := os.Lstat(path); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, &os.PathError{Op: "open", Path: path, Err: ErrLink}
		}
	}
	return f, err
}
