package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	if a.ID != "github.com-not-three-ui" || !a.Enabled || a.EffectiveMerge.Story != "merge" || a.Merge != (api.MergeModes{}) || a.Root != "" {
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
	want := Change{Section: SectionProjects, Name: "x", FromDisk: true}
	if len(changes) != 1 || !reflect.DeepEqual(changes[0], want) {
		t.Fatalf("changes = %+v", changes)
	}
	if got := log.take(); !reflect.DeepEqual(got, changes) {
		t.Fatalf("OnChange got %+v", got)
	}
	p, ok := s.Project("x")
	if !ok || p.Path != repo || !p.Enabled || p.EffectiveMerge.Story != "merge" {
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
	if got, ok := s.Project(p.ID); !ok || got.EffectiveMerge.Story != "merge" {
		t.Fatalf("project = %+v %v", got, ok)
	}
	// Fixing it through the store clears the error.
	merge := "squash"
	if _, err := s.UpdateProject(p.ID, ProjectUpdate{MergeStory: &merge}); err != nil {
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
	for _, u := range []ProjectUpdate{{MergeStory: &bad}, {MergeTask: &bad}, {MergeStoryTask: &bad}} {
		_, err := s.UpdateProject(a.ID, u)
		wantCode(t, err, api.CodeValidation)
	}
	empty := " "
	_, err := s.UpdateProject(a.ID, ProjectUpdate{Name: &empty})
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
	fakeFolderCheck(t, func(path string) bool { return path == "hung" }, func(string) bool { return true })
	got := missingPaths(map[string]string{"a": "ok", "b": "hung"}, 200*time.Millisecond)
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("got %v, want only the answered folder", got)
	}
}

// TestMissingFoldersKeepsAnswersAfterAHungOne: whichever folder is waited
// on first, every folder that answered within the time limit is reported.
func TestMissingFoldersKeepsAnswersAfterAHungOne(t *testing.T) {
	fakeFolderCheck(t, func(path string) bool { return path == "hung" }, func(string) bool { return true })
	paths := []string{"hung"}
	for i := range 20 {
		paths = append(paths, fmt.Sprintf("answered-%d", i))
	}
	got := missingFolders(paths, 100*time.Millisecond)
	if len(got) != 20 || got["hung"] {
		t.Fatalf("got %d answers %v, want the 20 answered folders", len(got), got)
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
	want := `"a/one" lies under the workspace root ` + ws + " and would be found again at the next scan. Switch it off, or add an ignore pattern to the root."
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

func TestProjectNamesRefuseHiddenCharacters(t *testing.T) {
	s, _, _ := openTest(t)
	p, err := s.AddProject(newRepo(t, t.TempDir(), "r"), "ok")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hiddenSamples {
		name := "a" + h + "b"
		_, err := s.AddProject(newRepo(t, t.TempDir(), "n"), name)
		wantCode(t, err, api.CodeValidation)
		if fieldOf(err) != "name" {
			t.Errorf("add %q: field = %q", name, fieldOf(err))
		}
		_, err = s.UpdateProject(p.ID, ProjectUpdate{Name: &name})
		wantCode(t, err, api.CodeValidation)
		if fieldOf(err) != "name" {
			t.Errorf("rename %q: field = %q", name, fieldOf(err))
		}
	}
	if got, _ := s.Project(p.ID); got.Name != "ok" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestProjectWithHiddenNameOnDiskStillLoads(t *testing.T) {
	s, home, _ := openTest(t)
	repo := newRepo(t, t.TempDir(), "r")
	path := filepath.Join(home, "projects", "x.yaml")
	writeFile(t, path, "name: \"a\\u202eb\\u200b\"\npath: "+repo+"\n")
	s.Reload()
	p, ok := s.Project("x")
	if !ok || p.Name != "a-b-" || len(s.Errors()) != 0 {
		t.Fatalf("project = %+v %v, errors %+v", p, ok, s.Errors())
	}
	// Its other fields can still be changed, which stores the shown name.
	off := false
	if _, err := s.UpdateProject("x", ProjectUpdate{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "name: a-b-\n") {
		t.Fatalf("file = %s", raw)
	}
	name := "ab"
	if got, err := s.UpdateProject("x", ProjectUpdate{Name: &name}); err != nil || got.Name != "ab" {
		t.Fatalf("rename = %+v %v", got, err)
	}
}

func TestAddProjectDefaultNameDropsHiddenCharacters(t *testing.T) {
	s, _, _ := openTest(t)
	repo := newRepo(t, t.TempDir(), "a\u202eb")
	p, err := s.AddProject(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "a-b" {
		t.Fatalf("name = %q", p.Name)
	}
}

// TestProjectListsDoNotWaitForAHungFolder lists projects while one folder
// does not answer: the lists come back within the folder check's bound with
// that project not reported missing, and a folder already being checked is
// not checked a second time.
func TestProjectListsDoNotWaitForAHungFolder(t *testing.T) {
	s, _, _ := openTest(t)
	parent := t.TempDir()
	ok, err := s.AddProject(newRepo(t, parent, "ok"), "")
	if err != nil {
		t.Fatal(err)
	}
	hung, err := s.AddProject(newRepo(t, parent, "hung"), "")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	fakeFolderCheck(t, func(path string) bool {
		mu.Lock()
		calls[path]++
		mu.Unlock()
		return path == hung.Path
	}, projectMissing)
	oldTimeout := folderCheckTimeout
	folderCheckTimeout = 100 * time.Millisecond
	t.Cleanup(func() { folderCheckTimeout = oldTimeout })

	for name, list := range map[string]func() []Project{
		"Projects":       func() []Project { return s.Projects(true) },
		"SearchProjects": func() []Project { return s.SearchProjects("") },
		"Project": func() []Project {
			a, _ := s.Project(ok.ID)
			b, _ := s.Project(hung.ID)
			return []Project{a, b}
		},
	} {
		start := time.Now()
		got := list()
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s took %s", name, d)
		}
		if len(got) != 2 {
			t.Fatalf("%s = %+v", name, got)
		}
		for _, p := range got {
			if p.Missing {
				t.Errorf("%s: %s reported missing", name, p.ID)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls[hung.Path] != 1 {
		t.Errorf("the hung folder was checked %d times, want once while its check runs", calls[hung.Path])
	}
}

// fakeFolderCheck replaces checkFolder: a folder for which hangs reports true
// does not answer until the test ends (then it is "missing" = true); every
// other folder is answered by answer. Folder checks are shared by the whole
// package (a check still running is joined, not repeated), so at the end the
// test waits until its released checks have finished: a later test asking
// for a folder of the same name must start a check of its own instead of
// joining this test's, which would answer at once.
func fakeFolderCheck(t *testing.T, hangs func(path string) bool, answer func(path string) bool) {
	t.Helper()
	release := make(chan struct{})
	checkFolder = func(path string) bool {
		if hangs(path) {
			<-release
			return true
		}
		return answer(path)
	}
	t.Cleanup(func() {
		close(release)
		checkFolder = projectMissing
		deadline := time.Now().Add(5 * time.Second)
		for {
			folderChecks.mu.Lock()
			n := len(folderChecks.running)
			folderChecks.mu.Unlock()
			if n == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("%d folder checks still run after the test", n)
				return
			}
			time.Sleep(time.Millisecond)
		}
	})
}

// TestFolderCheckFakesGoThroughTheHelper: a test that replaces checkFolder by
// hand leaves its hung checks in the shared list, and the next test asking
// for the same folder gets their answer. Only fakeFolderCheck assigns a fake.
func TestFolderCheckFakesGoThroughTheHelper(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fakes := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "checkFolder = ") || trimmed == "checkFolder = projectMissing" {
				continue
			}
			if fakes++; fakes > 1 {
				t.Errorf("%s:%d replaces checkFolder; use fakeFolderCheck", f, i+1)
			}
		}
	}
}
