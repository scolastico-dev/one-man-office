package settings

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// Patterns lists the settings files of the store relative to the home, with
// slashes: "settings.yaml" for a single file, "<folder>/*<ext>" for a folder
// with one file per item. A settings zip carries exactly these (see
// internal/homezip), and a test there fails when the two lists differ.
func (s *Store) Patterns() []string {
	out := make([]string, 0, len(s.kinds))
	for _, k := range s.kinds {
		if k.dir == "" {
			out = append(out, s.relPath(k.file))
			continue
		}
		out = append(out, s.relPath(k.dir)+"/*"+k.ext)
	}
	sort.Strings(out)
	return out
}

// ItemFile reports whether rel (relative to the home, with slashes) names a
// settings file of the store whose name the store would load, and the largest
// size it may have.
func (s *Store) ItemFile(rel string) (limit int64, ok bool) {
	k, _, ok := s.kindOf(rel)
	if !ok {
		return 0, false
	}
	return k.limit, true
}

// kindOf finds the section of a settings file named relative to the home.
func (s *Store) kindOf(rel string) (*kind, string, bool) {
	if rel == "" || strings.Contains(rel, `\`) || path.Clean(rel) != rel || path.IsAbs(rel) {
		return nil, "", false
	}
	for _, k := range s.kinds {
		if k.dir == "" {
			if s.relPath(k.file) == rel {
				return k, "", true
			}
			continue
		}
		dir, file := path.Split(rel)
		if strings.TrimSuffix(dir, "/") != s.relPath(k.dir) || !strings.HasSuffix(file, k.ext) || ignoredFile(file) {
			continue
		}
		name := strings.TrimSuffix(file, k.ext)
		if !k.namePattern().MatchString(name) {
			return nil, "", false
		}
		return k, name, true
	}
	return nil, "", false
}

// CheckFiles loads files (relative path with slashes → content) the way
// they would load next to the current settings and reports every file that
// would not load: one of files, or a current file that loads now and would
// stop loading beside them. It works on a copy in scratch, an empty folder
// the caller removes afterwards; nothing of the home changes. Each
// FileError's File is relative to the home, with slashes. A path that is no
// settings file of the store is reported too.
func (s *Store) CheckFiles(scratch string, files map[string][]byte) []FileError {
	var out []FileError
	broken := map[string]bool{}
	s.mu.RLock()
	for p, f := range s.files {
		if f.err != nil {
			broken[s.relPath(p)] = true
		}
	}
	// The current settings files, as they are on disk.
	var current []string
	for _, k := range s.kinds {
		if k.dir == "" {
			current = append(current, s.relPath(k.file))
			continue
		}
		entries, _ := os.ReadDir(k.dir)
		for _, e := range entries {
			rel := s.relPath(filepath.Join(k.dir, e.Name()))
			if _, _, ok := s.kindOf(rel); ok && e.Type().IsRegular() {
				current = append(current, rel)
			}
		}
	}
	limits := map[string]int64{}
	for _, rel := range current {
		k, _, _ := s.kindOf(rel)
		limits[rel] = k.limit
	}
	s.mu.RUnlock()

	write := func(rel string, data []byte) error {
		p := filepath.Join(scratch, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		return os.WriteFile(p, data, 0o600)
	}
	for _, rel := range current {
		if _, replaced := files[rel]; replaced {
			continue
		}
		data, err := homefile.Read(filepath.Join(s.root, filepath.FromSlash(rel)), limits[rel])
		if err != nil {
			// Broken now; the store reports it already.
			continue
		}
		if err := write(rel, data); err != nil {
			return []FileError{{File: rel, Message: "the check could not be prepared: " + err.Error()}}
		}
	}
	names := make([]string, 0, len(files))
	for rel := range files {
		names = append(names, rel)
	}
	sort.Strings(names)
	for _, rel := range names {
		k, _, ok := s.kindOf(rel)
		switch {
		case !ok:
			out = append(out, FileError{File: rel, Message: "this is not a settings file omo knows"})
			continue
		case int64(len(files[rel])) > k.limit:
			msg, _ := homefile.Refusal(homefile.ErrTooLarge, k.limit)
			out = append(out, FileError{File: rel, Message: msg})
			continue
		}
		if err := write(rel, files[rel]); err != nil {
			return []FileError{{File: rel, Message: "the check could not be prepared: " + err.Error()}}
		}
	}
	if len(out) > 0 {
		return out
	}
	o := s.opts
	o.OnChange = nil
	o.ReservedProjectIDs = nil
	check, err := Open(scratch, o)
	if err != nil {
		return []FileError{{File: "", Message: "the settings could not be checked: " + err.Error()}}
	}
	for _, e := range check.Errors() {
		rel := check.relPath(e.File)
		if broken[rel] {
			continue
		}
		out = append(out, FileError{File: rel, Message: e.Message})
	}
	return out
}

// Replace runs write with the store's locks held, so no other settings
// write interleaves, and then loads the settings files at paths (relative
// to the home, with slashes) again, also when write failed (it may have put
// back what it had replaced). The changes are delivered as the store's own,
// not as hand edits, like those of a save. write replaces files on disk and
// must not call the store.
func (s *Store) Replace(paths []string, write func() error) error {
	var err error
	s.mutate(func() ([]Change, error) {
		err = write()
		for _, rel := range paths {
			delete(s.files, filepath.Join(s.root, filepath.FromSlash(rel)))
		}
		changes, scanErr := s.scan()
		if err == nil {
			err = scanErr
		}
		return changes, nil
	})
	return err
}
