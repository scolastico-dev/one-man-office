package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Project is a registered project. Missing is computed on every read: the
// folder is absent, not a folder, or has no .git.
type Project = api.Project

// ProjectUpdate changes a project; nil fields stay unchanged.
type ProjectUpdate struct {
	Name       *string
	StoryMerge *string
	Enabled    *bool
}

// projectFile is the content of projects/<id>.yaml.
type projectFile struct {
	Name       string `yaml:"name"`
	Path       string `yaml:"path"`
	Enabled    bool   `yaml:"enabled"`
	Root       string `yaml:"root"`
	StoryMerge string `yaml:"story_merge"`
}

func (s *Store) projectDir() string { return filepath.Join(s.root, "projects") }

func (s *Store) projectPath(id string) string {
	return filepath.Join(s.projectDir(), id+".yaml")
}

func (s *Store) projectKind() *kind {
	return &kind{
		section: SectionProjects,
		dir:     s.projectDir(),
		ext:     ".yaml",
		load: func(id string, data []byte) error {
			f := projectFile{Enabled: true, StoryMerge: api.StoryMergeMerge}
			if err := decodeYAML(data, &f); err != nil {
				return err
			}
			p := Project{ID: id, Name: f.Name, Path: ExpandPath(f.Path), Enabled: f.Enabled, Root: f.Root, StoryMerge: f.StoryMerge}
			if err := validateProject(p); err != nil {
				return err
			}
			s.projects[id] = p
			return nil
		},
		drop: func(id string) bool {
			_, ok := s.projects[id]
			delete(s.projects, id)
			return ok
		},
	}
}

func validateProject(p Project) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name must not be empty")
	}
	if strings.ContainsAny(p.Name, "\n\r") {
		return errors.New("name must be a single line")
	}
	if !filepath.IsAbs(p.Path) {
		return fmt.Errorf("path must be absolute, got %q", p.Path)
	}
	if p.StoryMerge != api.StoryMergeMerge && p.StoryMerge != api.StoryMergeSquash {
		return fmt.Errorf("story_merge must be merge or squash, got %q", p.StoryMerge)
	}
	return nil
}

// projectMissing reports whether a project's folder is gone or is no git
// repository.
func projectMissing(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return true
	}
	_, err = os.Stat(filepath.Join(path, ".git"))
	return err != nil
}

// withMissing computes Missing for each project.
func withMissing(ps []Project) []Project {
	for i := range ps {
		ps[i].Missing = projectMissing(ps[i].Path)
	}
	return ps
}

// Projects lists the projects sorted by name (ignoring case), without disabled ones unless
// includeDisabled is set.
func (s *Store) Projects(includeDisabled bool) []Project {
	return withMissing(s.listProjects(func(p Project) bool { return includeDisabled || p.Enabled }))
}

// Project returns one project by id.
func (s *Store) Project(id string) (Project, bool) {
	s.mu.RLock()
	p, ok := s.projects[id]
	s.mu.RUnlock()
	if ok {
		p.Missing = projectMissing(p.Path)
	}
	return p, ok
}

// SearchProjects lists the enabled projects whose name or path contains q,
// ignoring case.
func (s *Store) SearchProjects(q string) []Project {
	return withMissing(s.listProjects(func(p Project) bool { return p.Enabled && MatchProject(p, q) }))
}

// MatchProject reports whether the project's name or path contains q,
// ignoring case. An empty q matches every project.
func MatchProject(p Project, q string) bool {
	q = strings.ToLower(q)
	return strings.Contains(strings.ToLower(p.Name), q) || strings.Contains(strings.ToLower(p.Path), q)
}

func (s *Store) listProjects(keep func(Project) bool) []Project {
	s.mu.RLock()
	out := []Project{}
	for _, p := range s.projects {
		if keep(p) {
			out = append(out, p)
		}
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// AddProject registers the git repository at path. The path must be absolute
// (a leading ~ is expanded) and is stored with symlinks resolved. An empty
// name defaults to the folder's base name.
func (s *Store) AddProject(path, name string) (Project, error) {
	path = ExpandPath(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return Project{}, api.Errorf(api.CodeValidation, "path must be absolute, got %q", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Project{}, api.Errorf(api.CodeValidation, "path %s cannot be resolved: %v", path, err)
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return Project{}, api.Errorf(api.CodeValidation, "path %s is not a folder", path)
	}
	if _, err := os.Stat(filepath.Join(real, ".git")); err != nil {
		return Project{}, api.Errorf(api.CodeValidation, "path %s is not a git repository (no .git)", path)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(real)
	}
	p := Project{Name: name, Path: real, Enabled: true, StoryMerge: api.StoryMergeMerge}
	if err := validateProject(p); err != nil {
		return Project{}, api.Errorf(api.CodeValidation, "%v", err)
	}
	err = s.mutate(func() ([]Change, error) {
		for _, other := range s.projects {
			if samePath(other.Path, real) {
				return nil, api.Errorf(api.CodeConflict, "path %s is already the project %s", path, other.ID)
			}
		}
		p.ID = projectID(name, func(id string) bool {
			_, loaded := s.projects[id]
			return loaded || fileExists(s.projectPath(id))
		})
		if err := s.saveProject(p); err != nil {
			return nil, err
		}
		return []Change{{Section: SectionProjects, Name: p.ID}}, nil
	})
	if err != nil {
		return Project{}, err
	}
	p.Missing = projectMissing(p.Path)
	return p, nil
}

// UpdateProject changes a project's name, enabled switch or story merge mode.
// Its id and file name never change.
func (s *Store) UpdateProject(id string, u ProjectUpdate) (Project, error) {
	var p Project
	err := s.mutate(func() ([]Change, error) {
		var ok bool
		p, ok = s.projects[id]
		if !ok {
			return nil, api.Errorf(api.CodeNotFound, "no project %s", id)
		}
		if u.Name != nil {
			p.Name = strings.TrimSpace(*u.Name)
		}
		if u.StoryMerge != nil {
			p.StoryMerge = *u.StoryMerge
		}
		if u.Enabled != nil {
			p.Enabled = *u.Enabled
		}
		if err := validateProject(p); err != nil {
			return nil, api.Errorf(api.CodeValidation, "%v", err)
		}
		if err := s.saveProject(p); err != nil {
			return nil, err
		}
		return []Change{{Section: SectionProjects, Name: id}}, nil
	})
	if err != nil {
		return Project{}, err
	}
	p.Missing = projectMissing(p.Path)
	return p, nil
}

// RemoveProject deletes a project's file. A file that could not be loaded can
// be removed too.
func (s *Store) RemoveProject(id string) error {
	return s.mutate(func() ([]Change, error) {
		_, loaded := s.projects[id]
		path := s.projectPath(id)
		if !idPattern.MatchString(id) || (!loaded && !fileExists(path)) {
			return nil, api.Errorf(api.CodeNotFound, "no project %s", id)
		}
		if err := s.removeFile(path); err != nil {
			return nil, err
		}
		delete(s.projects, id)
		return []Change{{Section: SectionProjects, Name: id, Removed: true}}, nil
	})
}

// RemoveMissingProjects deletes the files of every missing project and
// returns their ids, sorted.
func (s *Store) RemoveMissingProjects() ([]string, error) {
	removed := []string{}
	err := s.mutate(func() ([]Change, error) {
		var changes []Change
		for id, p := range s.projects {
			if !projectMissing(p.Path) {
				continue
			}
			if err := s.removeFile(s.projectPath(id)); err != nil {
				return changes, err
			}
			delete(s.projects, id)
			removed = append(removed, id)
		}
		sort.Strings(removed)
		for _, id := range removed {
			changes = append(changes, Change{Section: SectionProjects, Name: id, Removed: true})
		}
		return changes, nil
	})
	return removed, err
}

// saveProject writes a project's file and makes it the loaded content. It
// runs with the state lock held.
func (s *Store) saveProject(p Project) error {
	data, err := encodeYAML(projectFile{Name: p.Name, Path: p.Path, Enabled: p.Enabled, Root: p.Root, StoryMerge: p.StoryMerge})
	if err != nil {
		return api.Errorf(api.CodeInternal, "encode project: %v", err)
	}
	if err := s.writeFile(s.projectPath(p.ID), data); err != nil {
		return err
	}
	p.Missing = false
	s.projects[p.ID] = p
	return nil
}

func samePath(stored, real string) bool {
	if r, err := filepath.EvalSymlinks(stored); err == nil {
		stored = r
	}
	return filepath.Clean(stored) == filepath.Clean(real)
}

// windowsReserved are base names Windows does not allow as file names.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// projectID derives an id from a project name: lowercase, every run of
// characters outside [a-z0-9._-] becomes "-", leading characters other than
// a letter or digit and trailing "-" are trimmed, and "-2", "-3", ... is
// appended while taken reports the id as used.
func projectID(name string, taken func(string) bool) string {
	var b strings.Builder
	run := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			run = false
		} else if !run {
			b.WriteByte('-')
			run = true
		}
	}
	base := strings.TrimLeftFunc(b.String(), func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') })
	base = strings.TrimRight(base, "-")
	if base == "" {
		base = "project"
	}
	for n := 1; ; n++ {
		suffix := ""
		if n > 1 {
			suffix = "-" + strconv.Itoa(n)
		}
		id := base
		if len(id)+len(suffix) > 64 {
			id = strings.TrimRight(id[:64-len(suffix)], "-")
		}
		id += suffix
		stem, _, _ := strings.Cut(id, ".")
		if windowsReserved[stem] || taken(id) {
			continue
		}
		return id
	}
}
