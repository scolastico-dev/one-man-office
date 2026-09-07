package globalhome

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type templateItem struct {
	rel  string
	mode fs.FileMode
	dir  bool
	data []byte
}

// Template is a validated in-memory snapshot of the user template. Preparing
// it before setup writes anything prevents bad source files from initializing
// an office and avoids mixing source revisions during the subsequent copy.
type Template struct{ items []templateItem }

const officeConfigPath = ".omo/omo.yaml"

// ConfigOverride returns the reserved partial office-config template, if any.
func (t *Template) ConfigOverride() ([]byte, bool) {
	for _, item := range t.items {
		if filepath.ToSlash(item.rel) == officeConfigPath && !item.dir {
			return append([]byte(nil), item.data...), true
		}
	}
	return nil, false
}

// ApplyTemplate prepares and overlays regular files onto an office root.
func (h *Home) ApplyTemplate(office string) ([]string, error) {
	template, err := h.PrepareTemplate(office)
	if err != nil {
		return nil, err
	}
	return template.Apply(office)
}

// PrepareTemplate reads every source file and validates existing destinations
// without modifying the office. The office itself may not exist yet.
func (h *Home) PrepareTemplate(office string) (*Template, error) {
	source := filepath.Join(h.Dir, "template")
	template := &Template{}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
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
		item := templateItem{rel: rel, mode: info.Mode().Perm(), dir: info.IsDir()}
		if !item.dir {
			item.data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		template.items = append(template.items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := template.validateDestinations(func(path string) (fs.FileInfo, error) { return os.Lstat(filepath.Join(office, path)) }); err != nil {
		return nil, err
	}
	return template, nil
}

func (t *Template) validateDestinations(lstat func(string) (fs.FileInfo, error)) error {
	for _, entry := range t.items {
		parts := strings.Split(entry.rel, string(filepath.Separator))
		for i := range parts {
			dest := filepath.Join(parts[:i+1]...)
			existing, err := lstat(dest)
			if os.IsNotExist(err) {
				break
			}
			if err != nil {
				return err
			}
			if existing.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("template destination is a symbolic link: %s", dest)
			}
			wantDir := i < len(parts)-1 || entry.dir
			if existing.IsDir() != wantDir || (!wantDir && !existing.Mode().IsRegular()) {
				return fmt.Errorf("template destination has incompatible type: %s", dest)
			}
		}
	}
	return nil
}

// Apply rechecks the destination after embedded setup, then copies the prepared
// bytes. On an I/O failure the caller may retry this same snapshot.
func (t *Template) Apply(office string) ([]string, error) {
	root, err := os.OpenRoot(office)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := t.validateDestinations(root.Lstat); err != nil {
		return nil, err
	}
	var copied []string
	for _, entry := range t.items {
		if filepath.ToSlash(entry.rel) == officeConfigPath {
			continue
		}
		if entry.dir {
			if err := root.MkdirAll(entry.rel, entry.mode); err != nil {
				return copied, err
			}
			continue
		}
		f, err := root.OpenFile(entry.rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, entry.mode)
		if err != nil {
			return copied, err
		}
		_, writeErr := f.Write(entry.data)
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
