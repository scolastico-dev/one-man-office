package globalhome

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ApplyTemplate overlays regular files onto an office root. Validate the
// complete tree first so unsupported links/types never produce partial copies.
func (h *Home) ApplyTemplate(office string) ([]string, error) {
	source := filepath.Join(h.Dir, "template")
	root, err := os.OpenRoot(office)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	type item struct {
		rel  string
		mode fs.FileMode
		dir  bool
	}
	var items []item
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("template entry must be a regular file or directory: %s", rel)
		}
		if rel == "." {
			return nil
		}
		// Every existing ancestor must be a real directory, never a symlink.
		parts := strings.Split(rel, string(filepath.Separator))
		for i := range parts {
			dest := filepath.Join(parts[:i+1]...)
			existing, err := root.Lstat(dest)
			if os.IsNotExist(err) {
				break
			}
			if err != nil {
				return err
			}
			if existing.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("template destination is a symbolic link: %s", dest)
			}
			wantDir := i < len(parts)-1 || info.IsDir()
			if existing.IsDir() != wantDir || (!wantDir && !existing.Mode().IsRegular()) {
				return fmt.Errorf("template destination has incompatible type: %s", dest)
			}
		}
		items = append(items, item{rel: rel, mode: info.Mode().Perm(), dir: info.IsDir()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	var copied []string
	for _, entry := range items {
		if entry.dir {
			if err := root.MkdirAll(entry.rel, entry.mode); err != nil {
				return copied, err
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.rel))
		if err != nil {
			return copied, err
		}
		f, err := root.OpenFile(entry.rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, entry.mode)
		if err != nil {
			return copied, err
		}
		_, writeErr := f.Write(data)
		chmodErr := f.Chmod(entry.mode)
		closeErr := f.Close()
		if writeErr != nil {
			return copied, writeErr
		}
		if chmodErr != nil {
			return copied, chmodErr
		}
		if closeErr != nil {
			return copied, closeErr
		}
		copied = append(copied, filepath.ToSlash(entry.rel))
	}
	return copied, nil
}
