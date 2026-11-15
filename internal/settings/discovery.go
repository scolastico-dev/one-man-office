package settings

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
	"gopkg.in/yaml.v3"
)

// Found is a repository found below a workspace root: its absolute path and
// its slash-separated path below the root ("" for the root itself).
type Found struct{ Path, Rel string }

// maxBrokenProjectFile is the most of a broken project file read to find its
// path.
const maxBrokenProjectFile = 1 << 20

// DiscoverProjects adds a project for every found repository whose folder is
// not known yet and returns the added projects. Known are the folders of
// every loaded project and of every project file that cannot be loaded but
// whose path can still be read, compared with links resolved: a rename, a
// switched-off project or a missing one is never touched or added again.
// A new project is named by its path below the root (the root itself by its
// base name), with hidden characters replaced by "-"; its id is never one a
// ticket references. root is the workspace root as written in the settings.
func (s *Store) DiscoverProjects(root string, found []Found) ([]Project, error) {
	added := []Project{}
	if len(found) == 0 {
		return added, nil
	}
	reserved := s.reservedProjectIDs()

	// Resolve the paths outside the locks; projects added meanwhile are
	// resolved under the lock.
	s.mu.RLock()
	paths := make([]string, 0, len(s.projects)+len(found))
	for _, p := range s.projects {
		paths = append(paths, p.Path)
	}
	s.mu.RUnlock()
	for _, f := range found {
		paths = append(paths, f.Path)
	}
	resolved := resolvePaths(paths)

	err := s.mutate(func() ([]Change, error) {
		known := map[string]bool{}
		var unresolved []string
		mark := func(p string) {
			if r, ok := resolved[p]; ok {
				known[r] = true
				known[filepath.Clean(p)] = true
			} else {
				unresolved = append(unresolved, p)
			}
		}
		for _, p := range s.projects {
			mark(p.Path)
		}
		for _, p := range s.brokenProjectPaths() {
			mark(p)
		}
		for p, r := range resolvePaths(unresolved) {
			known[r] = true
			known[filepath.Clean(p)] = true
		}

		var changes []Change
		for _, f := range found {
			path := filepath.Clean(f.Path)
			if !filepath.IsAbs(f.Path) || known[path] || known[resolved[f.Path]] {
				continue
			}
			p := Project{
				Name:       discoveredName(root, f),
				Path:       path,
				Enabled:    true,
				Root:       root,
				StoryMerge: api.StoryMergeMerge,
			}
			if err := validateProject(p); err != nil {
				continue
			}
			p.ID = s.newProjectID(p.Name, reserved)
			if err := s.saveProject(p); err != nil {
				return changes, err
			}
			known[path] = true
			known[resolved[f.Path]] = true
			added = append(added, p)
			changes = append(changes, Change{Section: SectionProjects, Name: p.ID})
		}
		return changes, nil
	})
	for i := range added {
		added[i].Missing = projectMissing(added[i].Path)
	}
	return added, err
}

// discoveredName is the name of a found project: its path below the root, or
// the root's base name for the root itself, on one line with every hidden
// character and every byte that is not UTF-8 replaced by "-".
func discoveredName(root string, f Found) string {
	name := f.Rel
	if name == "" {
		name = filepath.Base(filepath.Clean(ExpandPath(root)))
		if name == "." || name == string(filepath.Separator) {
			name = filepath.Base(filepath.Clean(f.Path))
		}
	}
	var b strings.Builder
	for len(name) > 0 {
		r, size := utf8.DecodeRuneInString(name)
		name = name[size:]
		if (r == utf8.RuneError && size == 1) || textsafe.IsHidden(r) {
			b.WriteByte('-')
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == string(filepath.Separator) {
		return "project"
	}
	return out
}

// brokenProjectPaths returns the paths named by project files that could not
// be loaded, as far as a lenient reading finds one. It runs with the state
// lock held.
func (s *Store) brokenProjectPaths() []string {
	dir := s.projectDir()
	var out []string
	for file, st := range s.files {
		if st.err == nil || filepath.Dir(file) != dir {
			continue
		}
		if p, ok := readProjectPath(file); ok {
			out = append(out, p)
		}
	}
	return out
}

// readProjectPath reads the path key of a project file that may be broken:
// first as YAML ignoring every other key and type error, then line by line.
func readProjectPath(file string) (string, bool) {
	f, err := os.Open(file)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBrokenProjectFile))
	if err != nil {
		return "", false
	}
	var v struct {
		Path string `yaml:"path"`
	}
	var te *yaml.TypeError
	if err := yaml.Unmarshal(data, &v); (err == nil || errors.As(err, &te)) && strings.TrimSpace(v.Path) != "" {
		return cleanStoredPath(v.Path)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), maxBrokenProjectFile)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(sc.Text(), "path:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if u, err := strconv.Unquote(rest); err == nil && strings.HasPrefix(rest, `"`) {
			rest = u
		} else if len(rest) >= 2 && rest[0] == '\'' && rest[len(rest)-1] == '\'' {
			rest = strings.ReplaceAll(rest[1:len(rest)-1], "''", "'")
		}
		if rest != "" {
			return cleanStoredPath(rest)
		}
	}
	return "", false
}

// cleanStoredPath expands and cleans a path as the project loader does; a
// path that is not absolute names no folder.
func cleanStoredPath(p string) (string, bool) {
	p = filepath.Clean(ExpandPath(strings.TrimSpace(p)))
	return p, filepath.IsAbs(p)
}
