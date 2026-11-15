package globalhome

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to a temporary file next to path and renames it
// into place, so readers never see a partial file. A perm without group and
// other bits makes the file private to the user (a protected ACL on Windows).
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".omo-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if perm&0077 == 0 {
		if err := protectFile(f.Name()); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replaceFile(f.Name(), path)
}
