//go:build windows

package homefile

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// openNoFollow opens path for reading when its last element is not a link
// or another reparse point. Lstat decides first; the opened file must be the
// one Lstat saw.
func openNoFollow(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, &os.PathError{Op: "open", Path: path, Err: ErrLink}
	}
	if !before.Mode().IsRegular() {
		return nil, &os.PathError{Op: "open", Path: path, Err: ErrNotRegular}
	}
	// Unlike os.Open, share deletion as well as reads/writes: settings saves
	// and plugin tree swaps may rename a file while another goroutine reads it.
	// The existing handle still reads the same file after that rename.
	name, err := filepath.Abs(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	// Keep long local and UNC paths working when calling CreateFile directly.
	if !strings.HasPrefix(name, `\\?\`) && !strings.HasPrefix(name, `\\.\`) {
		if strings.HasPrefix(name, `\\`) {
			name = `\\?\UNC\` + name[2:]
		} else {
			name = `\\?\` + name
		}
	}
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(handle), path)
	after, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !os.SameFile(before, after) {
		f.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: ErrLink}
	}
	return f, nil
}

// openAppendNoFollow opens path for appending, created with perm, when its
// last element is not a link or another reparse point; the opened file
// must be the one Lstat saw.
func openAppendNoFollow(path string, perm os.FileMode) (*os.File, error) {
	before, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|os.O_EXCL, perm)
	case err != nil:
		return nil, err
	case before.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0:
		return nil, &os.PathError{Op: "open", Path: path, Err: ErrLink}
	case !before.Mode().IsRegular():
		return nil, &os.PathError{Op: "open", Path: path, Err: ErrNotRegular}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !os.SameFile(before, after) {
		f.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: ErrLink}
	}
	return f, nil
}
