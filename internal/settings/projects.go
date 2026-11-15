package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/discover"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
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
		limit:   homefile.MaxSettingsFile,
		load: func(id string, data []byte) error {
			f := projectFile{Enabled: true, StoryMerge: api.StoryMergeMerge}
			if err := decodeYAML(data, &f); err != nil {
				return err
			}
			p := Project{ID: id, Name: f.Name, Path: filepath.Clean(ExpandPath(f.Path)), Enabled: f.Enabled, Root: f.Root, StoryMerge: f.StoryMerge}
			if err := validateProject(p); err != nil {
				return err
			}
			// A name written by hand may hold hidden characters, which the
			// store refuses in names it sets: the project still loads, under
			// its name with them replaced, which the next write stores.
			p.Name = visibleName(p.Name)
			if other, ok := s.projectWithPath(id, p.Path); ok {
				return fmt.Errorf("the path is already the project %s", other)
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

// validateProject checks a project as loaded from its file. The error is a
// *FieldError naming the key; its message never quotes a value. Names set
// through the store are checked further by validateProjectName.
func validateProject(p Project) error {
	if strings.TrimSpace(p.Name) == "" {
		return fieldErr("name", "name must not be empty")
	}
	if strings.ContainsAny(p.Name, "\n\r") {
		return fieldErr("name", "name must be a single line")
	}
	if !filepath.IsAbs(p.Path) {
		return fieldErr("path", "path must be absolute")
	}
	if p.StoryMerge != api.StoryMergeMerge && p.StoryMerge != api.StoryMergeSquash {
		return fieldErr("story_merge", "story_merge must be merge or squash")
	}
	return nil
}

// validateProjectName checks a name set through the store (add, rename): no
// hidden characters on top of validateProject's rules. A file written by
// hand with such a name still loads, with them replaced (see projectKind).
func validateProjectName(name string) error {
	if !utf8.ValidString(name) {
		return fieldErr("name", "name must be UTF-8 text")
	}
	if hasHidden(name) {
		return fieldErr("name", "name must not hold control, invisible or direction characters")
	}
	return nil
}

// showName quotes a project's name for a message. Loaded names hold no
// hidden characters; anything else is escaped all the same.
func showName(name string) string {
	if utf8.ValidString(name) && !hasHidden(name) && !strings.Contains(name, `"`) {
		return `"` + name + `"`
	}
	return strconv.QuoteToASCII(name)
}

// visibleName replaces every hidden character and every byte that is not
// UTF-8 in s with "-": a folder's name made fit to be a project's name.
func visibleName(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if (r == utf8.RuneError && size == 1) || textsafe.IsHidden(r) {
			b.WriteByte('-')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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

// withMissing computes Missing for each project, all folders at once and
// within folderCheckTimeout (see missingFolders).
func withMissing(ps []Project) []Project {
	paths := make([]string, len(ps))
	for i, p := range ps {
		paths[i] = p.Path
	}
	missing := missingFolders(paths, folderCheckTimeout)
	for i := range ps {
		ps[i].Missing = missing[ps[i].Path]
	}
	return ps
}

// folderCheckTimeout bounds how long a project list waits for its folder
// checks, so a folder on a mount that does not answer cannot hold up the
// lists, the board's checks or the settings routes; tests shorten it.
var folderCheckTimeout = 2 * time.Second

// folderChecks are the folder checks under way, by path: a folder that does
// not answer is checked by one goroutine, however often it is asked for.
var folderChecks = struct {
	mu      sync.Mutex
	running map[string]*folderCheck
}{running: map[string]*folderCheck{}}

// folderCheck is one check of a folder; missing is set before done closes.
type folderCheck struct {
	done    chan struct{}
	missing bool
}

// missingFolders checks the folders of paths (with checkFolder) concurrently
// and reports which are missing. A folder whose check has not answered
// within timeout is not reported missing: a hung mount may come back. A
// folder whose earlier check still runs is not checked again; its answer is
// awaited.
func missingFolders(paths []string, timeout time.Duration) map[string]bool {
	out := make(map[string]bool, len(paths))
	checks := make(map[string]*folderCheck, len(paths))
	folderChecks.mu.Lock()
	for _, path := range paths {
		if _, ok := checks[path]; ok {
			continue
		}
		c, ok := folderChecks.running[path]
		if !ok {
			c = &folderCheck{done: make(chan struct{})}
			folderChecks.running[path] = c
			check := checkFolder
			go func() {
				c.missing = check(path)
				folderChecks.mu.Lock()
				delete(folderChecks.running, path)
				folderChecks.mu.Unlock()
				close(c.done)
			}()
		}
		checks[path] = c
	}
	folderChecks.mu.Unlock()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for path, c := range checks {
		select {
		case <-c.done:
			out[path] = c.missing
		case <-deadline.C:
			return out
		}
	}
	return out
}

// folderMissing is missingFolders for one folder.
func folderMissing(path string) bool {
	return missingFolders([]string{path}, folderCheckTimeout)[path]
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
		p.Missing = folderMissing(p.Path)
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
		name = strings.TrimSpace(visibleName(filepath.Base(real)))
	} else if err := validateProjectName(name); err != nil {
		return Project{}, apiError(api.CodeValidation, err)
	}
	p := Project{Name: name, Path: real, Enabled: true, StoryMerge: api.StoryMergeMerge}
	if err := validateProject(p); err != nil {
		return Project{}, apiError(api.CodeValidation, err)
	}
	reserved := s.reservedProjectIDs()
	err = s.mutate(func() ([]Change, error) {
		if other, ok := s.projectWithPath("", real); ok {
			return nil, api.Errorf(api.CodeConflict, "path %s is already the project %s", path, other)
		}
		p.ID = s.newProjectID(name, reserved)
		if err := s.saveProject(p); err != nil {
			return nil, err
		}
		return []Change{{Section: SectionProjects, Name: p.ID}}, nil
	})
	if err != nil {
		return Project{}, err
	}
	p.Missing = folderMissing(p.Path)
	return p, nil
}

// UpdateProject changes a project's name, enabled switch or story merge mode.
// Its id and file name never change. The project's file is read again first,
// so a hand edit not yet found by polling is kept in the fields not changed.
func (s *Store) UpdateProject(id string, u ProjectUpdate) (Project, error) {
	var p Project
	if u.Name != nil {
		if err := validateProjectName(strings.TrimSpace(*u.Name)); err != nil {
			return Project{}, apiError(api.CodeValidation, err)
		}
	}
	err := s.mutate(func() ([]Change, error) {
		if !idPattern.MatchString(id) {
			return nil, api.Errorf(api.CodeNotFound, "no project %s", showKey(id))
		}
		changes, err := s.guard(SectionProjects, id, "")
		if err != nil {
			return changes, err
		}
		var ok bool
		p, ok = s.projects[id]
		if !ok {
			return changes, api.Errorf(api.CodeNotFound, "no project %s", id)
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
			return changes, apiError(api.CodeValidation, err)
		}
		if err := s.saveProject(p); err != nil {
			return changes, err
		}
		return append(changes, Change{Section: SectionProjects, Name: id}), nil
	})
	if err != nil {
		return Project{}, err
	}
	p.Missing = folderMissing(p.Path)
	return p, nil
}

// RemoveProject deletes a project's file. A file that could not be loaded can
// be removed too. A present project found under a workspace root that is
// still in the settings is refused: the next scan would add it again.
func (s *Store) RemoveProject(id string) error {
	return s.mutate(func() ([]Change, error) {
		p, loaded := s.projects[id]
		path := s.projectPath(id)
		if !idPattern.MatchString(id) || (!loaded && !fileExists(path)) {
			return nil, api.Errorf(api.CodeNotFound, "no project %s", id)
		}
		if loaded {
			if root, ok := s.rootThatFinds(p); ok {
				return nil, api.Errorf(api.CodeValidation, "%s lies under the workspace root %s and would be found again at the next scan. Switch it off, or add an ignore pattern to the root.", showName(p.Name), root)
			}
		}
		if err := s.removeFile(path); err != nil {
			return nil, err
		}
		delete(s.projects, id)
		return []Change{{Section: SectionProjects, Name: id, Removed: true}}, nil
	})
}

// RemoveMissingProjects deletes the files of every missing project, in id
// order, and returns the ids removed. If a file cannot be removed, the
// projects removed before it are still notified and returned, together with
// the error.
func (s *Store) RemoveMissingProjects() ([]string, error) {
	// Check the folders without any lock: a hung mount must not block
	// settings reads.
	s.mu.RLock()
	snapshot := make(map[string]string, len(s.projects))
	for id, p := range s.projects {
		snapshot[id] = p.Path
	}
	s.mu.RUnlock()
	gone := missingPaths(snapshot, missingCheckTimeout)
	if afterMissingCheck != nil {
		afterMissingCheck(s)
	}
	removed := []string{}
	err := s.mutate(func() ([]Change, error) {
		missing := make([]string, 0, len(gone))
		for _, id := range gone {
			// Only a project still stored with the path that was checked.
			if p, ok := s.projects[id]; ok && p.Path == snapshot[id] {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		var changes []Change
		for _, id := range missing {
			if err := s.removeFile(s.projectPath(id)); err != nil {
				return changes, err
			}
			delete(s.projects, id)
			removed = append(removed, id)
			changes = append(changes, Change{Section: SectionProjects, Name: id, Removed: true})
		}
		return changes, nil
	})
	return removed, err
}

// missingCheckTimeout bounds the check of one project folder. A folder that
// does not answer in time is not reported missing: a hung mount may come
// back, and its project must not be deleted on a guess.
const missingCheckTimeout = 2 * time.Second

// afterMissingCheck lets tests change the store between the folder check and
// the removal.
var afterMissingCheck func(*Store)

// checkFolder is projectMissing; tests replace it to simulate a hung folder.
var checkFolder = projectMissing

// missingPaths returns the ids whose folder is missing, checking all folders
// concurrently with a per-folder timeout.
func missingPaths(paths map[string]string, timeout time.Duration) []string {
	list := make([]string, 0, len(paths))
	for _, path := range paths {
		list = append(list, path)
	}
	missing := missingFolders(list, timeout)
	var gone []string
	for id, path := range paths {
		if missing[path] {
			gone = append(gone, id)
		}
	}
	return gone
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

// reservedProjectIDs asks for the ids tickets reference. It must be called
// without the store's locks.
func (s *Store) reservedProjectIDs() map[string]bool {
	if s.opts.ReservedProjectIDs == nil {
		return nil
	}
	return s.opts.ReservedProjectIDs()
}

// newProjectID derives a free id from name: not loaded, no file on disk
// (loaded or not) and not referenced by a ticket. It runs with the state lock
// held.
func (s *Store) newProjectID(name string, reserved map[string]bool) string {
	return projectID(name, func(id string) bool {
		_, loaded := s.projects[id]
		return loaded || reserved[id] || fileExists(s.projectPath(id))
	})
}

// projectWithPath returns the id of a loaded project other than except whose
// folder is path, comparing with links resolved; the smallest id when there
// are several. It runs with the state lock held.
func (s *Store) projectWithPath(except, path string) (string, bool) {
	paths := []string{path}
	for id, p := range s.projects {
		if id != except {
			paths = append(paths, p.Path)
		}
	}
	resolved := resolvePaths(paths)
	want := resolved[path]
	found := ""
	for id, p := range s.projects {
		if id != except && resolved[p.Path] == want && (found == "" || id < found) {
			found = id
		}
	}
	return found, found != ""
}

// rootThatFinds returns the workspace root, as written in the settings, that
// a scan would find p under again: p was found under it, the root is still in
// the settings, and p's folder is present, lies below the root within its
// depth and is not ignored. It runs with the state lock held.
func (s *Store) rootThatFinds(p Project) (string, bool) {
	if p.Root == "" {
		return "", false
	}
	want := filepath.Clean(ExpandPath(p.Root))
	for _, r := range s.general.WorkspaceRoots {
		rootPath := filepath.Clean(ExpandPath(r.Path))
		if rootPath != want {
			continue
		}
		if gone := missingPaths(map[string]string{p.ID: p.Path}, missingCheckTimeout); len(gone) > 0 {
			return "", false
		}
		resolved := resolvePaths([]string{rootPath, p.Path})
		rel, err := filepath.Rel(resolved[rootPath], resolved[p.Path])
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", false
		}
		if rel == "." {
			rel = ""
		}
		if discover.Reachable(discover.Root{Path: rootPath, MaxDepth: r.MaxDepth, Ignore: r.Ignore}, filepath.ToSlash(rel)) {
			return r.Path, true
		}
		return "", false
	}
	return "", false
}

// resolveTimeout bounds resolving the links of project paths, so a folder on
// a mount that does not answer cannot hold the store's lock.
const resolveTimeout = 2 * time.Second

// resolvePaths maps each path to its cleaned form with links resolved. All
// paths are resolved concurrently within resolveTimeout; one that cannot be
// resolved, or not in time, maps to its cleaned form.
func resolvePaths(paths []string) map[string]string {
	type result struct{ path, real string }
	out := make(map[string]string, len(paths))
	pending := map[string]bool{}
	for _, p := range paths {
		out[p] = filepath.Clean(p)
		pending[p] = true
	}
	results := make(chan result, len(pending))
	for p := range pending {
		go func() {
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				real = filepath.Clean(p)
			}
			results <- result{p, real}
		}()
	}
	deadline := time.NewTimer(resolveTimeout)
	defer deadline.Stop()
	for range pending {
		select {
		case r := <-results:
			out[r.path] = r.real
		case <-deadline.C:
			return out
		}
	}
	return out
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
