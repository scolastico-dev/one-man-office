//go:build windows

package homefile

import "os"

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
	f, err := os.Open(path)
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
