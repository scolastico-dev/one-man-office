package settings

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func asAPIError(err error, target **api.Error) bool { return errors.As(err, target) }

// newRepo creates a folder that looks like a git repository.
func newRepo(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae *api.Error
	if !asAPIError(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func TestAddProjectGeneratesStableID(t *testing.T) {
	s, home, log := openTest(t)
	ws := t.TempDir()
	a, err := s.AddProject(newRepo(t, ws, "ui"), "github.com/not-three/ui")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "github.com-not-three-ui" || !a.Enabled || a.StoryMerge != "merge" || a.Root != "" {
		t.Fatalf("project = %+v", a)
	}
	b, err := s.AddProject(newRepo(t, ws, "ui2"), "github.com/not-three/ui")
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != "github.com-not-three-ui-2" {
		t.Fatalf("second id = %q", b.ID)
	}
	if ch := log.take(); len(ch) != 2 || ch[0].Section != SectionProjects || ch[0].Name != a.ID {
		t.Fatalf("changes = %+v", ch)
	}
	name := "renamed"
	u, err := s.UpdateProject(a.ID, ProjectUpdate{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != a.ID || u.Name != "renamed" {
		t.Fatalf("updated = %+v", u)
	}
	if _, err := os.Stat(filepath.Join(home, "projects", a.ID+".yaml")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(home, "projects"))
	if len(entries) != 2 {
		t.Fatalf("project files = %d, want 2", len(entries))
	}
	// The renamed file reads back the same.
	s2, err := Open(home, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Project(a.ID)
	if !ok || !reflect.DeepEqual(got, u) {
		t.Fatalf("reopened = %+v %v, want %+v", got, ok, u)
	}
}

func TestProjectIDFromName(t *testing.T) {
	cases := map[string]string{
		"github.com/not-three/ui": "github.com-not-three-ui",
		"My Project!!":            "my-project",
		"  --Weird__Name--  ":     "weird__name",
		"日本":                      "project",
		"con":                     "con-2",
	}
	for name, want := range cases {
		if got := projectID(name, func(string) bool { return false }); got != want {
			t.Errorf("projectID(%q) = %q, want %q", name, got, want)
		}
	}
	long := projectID("a"+strings.Repeat("b", 100), func(id string) bool { return !strings.HasSuffix(id, "-2") })
	if len(long) != 64 || !strings.HasSuffix(long, "b-2") || !idPattern.MatchString(long) {
		t.Errorf("long id = %q", long)
	}
}

func TestAddProjectDuplicatePathConflicts(t *testing.T) {
	s, _, _ := openTest(t)
	repo := newRepo(t, t.TempDir(), "r")
	if _, err := s.AddProject(repo, ""); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	_, err := s.AddProject(link, "other")
	wantCode(t, err, api.CodeConflict)
}

func TestAddProjectDefaultsNameToBase(t *testing.T) {
	s, _, _ := openTest(t)
	p, err := s.AddProject(newRepo(t, t.TempDir(), "My Repo"), "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "My Repo" || p.ID != "my-repo" {
		t.Fatalf("project = %+v", p)
	}
}

func TestAddProjectRejectsNonRepository(t *testing.T) {
	s, _, _ := openTest(t)
	_, err := s.AddProject(t.TempDir(), "x")
	wantCode(t, err, api.CodeValidation)
	_, err = s.AddProject("relative/path", "x")
	wantCode(t, err, api.CodeValidation)
	_, err = s.AddProject(filepath.Join(t.TempDir(), "absent"), "x")
	wantCode(t, err, api.CodeValidation)
}

func TestProjectMissingWhenFolderGone(t *testing.T) {
	s, home, log := openTest(t)
	repo := newRepo(t, t.TempDir(), "gone")
	p, err := s.AddProject(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Projects(true)[0].Missing {
		t.Fatal("present repository reported missing")
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if !s.Projects(true)[0].Missing {
		t.Fatal("missing not detected")
	}
	file := filepath.Join(home, "projects", p.ID+".yaml")
	if _, err := os.Stat(file); err != nil {
		t.Fatal("project file deleted automatically:", err)
	}
	log.take()
	removed, err := s.RemoveMissingProjects()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed, []string{p.ID}) {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("project file still exists")
	}
	if ch := log.take(); len(ch) != 1 || !ch[0].Removed || ch[0].Name != p.ID {
		t.Fatalf("changes = %+v", ch)
	}
}

func TestHandAddedProjectFileIsPickedUp(t *testing.T) {
	s, home, log := openTest(t)
	repo := newRepo(t, t.TempDir(), "x")
	writeFile(t, filepath.Join(home, "projects", "x.yaml"), "name: x\npath: "+repo+"\n")
	changes := s.Reload()
	want := Change{Section: SectionProjects, Name: "x"}
	if len(changes) != 1 || !reflect.DeepEqual(changes[0], want) {
		t.Fatalf("changes = %+v", changes)
	}
	if got := log.take(); !reflect.DeepEqual(got, changes) {
		t.Fatalf("OnChange got %+v", got)
	}
	p, ok := s.Project("x")
	if !ok || p.Path != repo || !p.Enabled || p.StoryMerge != "merge" {
		t.Fatalf("project = %+v %v", p, ok)
	}
	os.Remove(filepath.Join(home, "projects", "x.yaml"))
	changes = s.Reload()
	if len(changes) != 1 || !changes[0].Removed {
		t.Fatalf("changes = %+v", changes)
	}
	if _, ok := s.Project("x"); ok {
		t.Fatal("removed project still listed")
	}
}

func TestBrokenProjectEditKeepsProject(t *testing.T) {
	s, home, _ := openTest(t)
	p, err := s.AddProject(newRepo(t, t.TempDir(), "r"), "")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "projects", p.ID+".yaml"), "name: r\npath: "+p.Path+"\nstory_merge: rebase\n")
	changes := s.Reload()
	if len(changes) != 1 || changes[0].Err == nil || changes[0].Name != p.ID {
		t.Fatalf("changes = %+v", changes)
	}
	if got, ok := s.Project(p.ID); !ok || got.StoryMerge != "merge" {
		t.Fatalf("project = %+v %v", got, ok)
	}
	// Fixing it through the store clears the error.
	merge := "squash"
	if _, err := s.UpdateProject(p.ID, ProjectUpdate{StoryMerge: &merge}); err != nil {
		t.Fatal(err)
	}
	if len(s.Errors()) != 0 {
		t.Fatalf("errors = %+v", s.Errors())
	}
}

func TestProjectListingAndSearch(t *testing.T) {
	s, _, _ := openTest(t)
	ws := t.TempDir()
	a, _ := s.AddProject(newRepo(t, ws, "alpha"), "Zeta/Alpha")
	b, _ := s.AddProject(newRepo(t, ws, "beta"), "beta")
	off := false
	if _, err := s.UpdateProject(b.ID, ProjectUpdate{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if got := s.Projects(false); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("enabled = %+v", got)
	}
	if got := s.Projects(true); len(got) != 2 || got[0].ID != b.ID {
		t.Fatalf("all = %+v", got)
	}
	if got := s.SearchProjects("ALPHA"); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("search = %+v", got)
	}
	bad := "rebase"
	_, err := s.UpdateProject(a.ID, ProjectUpdate{StoryMerge: &bad})
	wantCode(t, err, api.CodeValidation)
	empty := " "
	_, err = s.UpdateProject(a.ID, ProjectUpdate{Name: &empty})
	wantCode(t, err, api.CodeValidation)
	_, err = s.UpdateProject("nope", ProjectUpdate{})
	wantCode(t, err, api.CodeNotFound)
	wantCode(t, s.RemoveProject("nope"), api.CodeNotFound)
	if err := s.RemoveProject(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Project(a.ID); ok {
		t.Fatal("removed project still present")
	}
}

func TestRemoveMissingProjectsReportsPartialFailure(t *testing.T) {
	s, home, log := openTest(t)
	parent := t.TempDir()
	var ids []string
	for _, name := range []string{"a", "b", "c"} {
		p, err := s.AddProject(newRepo(t, parent, name), name)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	if err := os.RemoveAll(parent); err != nil {
		t.Fatal(err)
	}
	// Turn the second project's file into a non-empty folder so removing it fails.
	second := filepath.Join(home, "projects", ids[1]+".yaml")
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(second, "keep"), "x")
	log.take()
	removed, err := s.RemoveMissingProjects()
	if err == nil {
		t.Fatal("want an error for the second project")
	}
	if !reflect.DeepEqual(removed, ids[:1]) {
		t.Fatalf("removed = %v, want %v", removed, ids[:1])
	}
	if c := log.take(); len(c) != 1 || c[0].Section != SectionProjects || c[0].Name != ids[0] || !c[0].Removed {
		t.Fatalf("changes = %+v", c)
	}
	if _, ok := s.Project(ids[0]); ok {
		t.Fatal("first project still loaded")
	}
	if _, ok := s.Project(ids[1]); !ok {
		t.Fatal("second project must stay loaded")
	}
}

func TestRemoveMissingProjectsSkipsAProjectWhosePathChanged(t *testing.T) {
	s, home, _ := openTest(t)
	parent := t.TempDir()
	gone := newRepo(t, parent, "gone")
	moved := newRepo(t, parent, "moved")
	other := newRepo(t, parent, "other")
	pg, err := s.AddProject(gone, "")
	if err != nil {
		t.Fatal(err)
	}
	pm, err := s.AddProject(moved, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{gone, moved} {
		if err := os.RemoveAll(r); err != nil {
			t.Fatal(err)
		}
	}
	// Between the check and the removal the moved project points elsewhere.
	afterMissingCheck = func(s *Store) {
		s.mu.Lock()
		p := s.projects[pm.ID]
		p.Path = other
		s.projects[pm.ID] = p
		s.mu.Unlock()
	}
	t.Cleanup(func() { afterMissingCheck = nil })
	removed, err := s.RemoveMissingProjects()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed, []string{pg.ID}) {
		t.Fatalf("removed = %v, want only %s", removed, pg.ID)
	}
	if _, ok := s.Project(pm.ID); !ok {
		t.Fatal("project with a changed path was removed")
	}
	if _, err := os.Stat(filepath.Join(home, "projects", pm.ID+".yaml")); err != nil {
		t.Fatal("its file was deleted:", err)
	}
}

func TestMissingPathsDoesNotWaitForAHungFolder(t *testing.T) {
	release := make(chan struct{})
	checkFolder = func(path string) bool {
		if path == "hung" {
			<-release
			return true
		}
		return true
	}
	t.Cleanup(func() { close(release); checkFolder = projectMissing })
	got := missingPaths(map[string]string{"a": "ok", "b": "hung"}, 200*time.Millisecond)
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("got %v, want only the answered folder", got)
	}
}

func TestDuplicatePathFileIsBroken(t *testing.T) {
	home := t.TempDir()
	repo := newRepo(t, t.TempDir(), "r")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	// Loaded in name order: a first, then b (through a link) and c (an
	// unclean spelling of the same folder).
	writeFile(t, filepath.Join(home, "projects", "a.yaml"), "name: a\npath: "+repo+"\n")
	writeFile(t, filepath.Join(home, "projects", "b.yaml"), "name: b\npath: "+link+"\n")
	writeFile(t, filepath.Join(home, "projects", "c.yaml"), "name: c\npath: "+repo+"/./x/..\n")
	s, err := Open(home, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Projects(true); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("projects = %+v", got)
	}
	errs := s.Errors()
	if len(errs) != 2 {
		t.Fatalf("errors = %+v", errs)
	}
	for _, e := range errs {
		if !strings.Contains(e.Message, "is already the project a") {
			t.Fatalf("message = %q", e.Message)
		}
	}
	// A hand-added duplicate later is reported too, the loaded one stays.
	writeFile(t, filepath.Join(home, "projects", "d.yaml"), "name: d\npath: "+link+"\n")
	changes := s.Reload()
	if len(changes) != 1 || changes[0].Err == nil || changes[0].Name != "d" {
		t.Fatalf("changes = %+v", changes)
	}
	if _, ok := s.Project("d"); ok {
		t.Fatal("duplicate loaded")
	}
	// Editing the loaded project's own file is not a duplicate of itself.
	writeFile(t, filepath.Join(home, "projects", "a.yaml"), "name: renamed\npath: "+repo+"\n")
	s.Reload()
	if p, _ := s.Project("a"); p.Name != "renamed" {
		t.Fatalf("project = %+v", p)
	}
}

func TestProjectPathCleanedOnLoad(t *testing.T) {
	s, home, _ := openTest(t)
	repo := newRepo(t, t.TempDir(), "r")
	writeFile(t, filepath.Join(home, "projects", "x.yaml"), "name: x\npath: "+repo+"/sub/../\n")
	s.Reload()
	if p, ok := s.Project("x"); !ok || p.Path != repo {
		t.Fatalf("project = %+v %v", p, ok)
	}
}

func TestRemoveDiscoveredProjectRefused(t *testing.T) {
	s, _, _ := openTest(t)
	ws := realTemp(t)
	repo := newRepo(t, ws, "a/one")
	newRepo(t, ws, "gone")
	g := s.General()
	g.WorkspaceRoots = []WorkspaceRoot{{Path: ws, MaxDepth: 4, Ignore: []string{"node_modules"}}}
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DiscoverProjects(ws, foundAt(ws, "a/one", "gone")); err != nil {
		t.Fatal(err)
	}
	err := s.RemoveProject("a-one")
	wantCode(t, err, api.CodeValidation)
	want := "#a-one lies under the workspace root " + ws + " and would be found again at the next scan. Switch it off, or add an ignore pattern to the root."
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("message = %q", err.Error())
	}
	if _, ok := s.Project("a-one"); !ok {
		t.Fatal("project removed")
	}

	// A missing project under its root can be removed.
	if err := os.RemoveAll(filepath.Join(ws, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveProject("gone"); err != nil {
		t.Fatal(err)
	}

	// An ignore pattern that hides it from the scan allows removing it.
	g.WorkspaceRoots[0].Ignore = []string{"a"}
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveProject("a-one"); err != nil {
		t.Fatal(err)
	}

	// Found again under the root: refused, until the root is removed from
	// the settings.
	g.WorkspaceRoots[0].Ignore = nil
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DiscoverProjects(ws, []Found{{Path: repo, Rel: "a/one"}}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.RemoveProject("a-one"), api.CodeValidation)
	g.WorkspaceRoots = nil
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveProject("a-one"); err != nil {
		t.Fatal(err)
	}

	// A project added by hand (no root) is removed as before.
	p, err := s.AddProject(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveProject(p.ID); err != nil {
		t.Fatal(err)
	}
}
