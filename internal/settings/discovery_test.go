package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// realTemp is a temporary folder with its links resolved.
func realTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func foundAt(root string, rels ...string) []Found {
	out := []Found{}
	for _, rel := range rels {
		p := root
		if rel != "" {
			p = filepath.Join(root, filepath.FromSlash(rel))
		}
		out = append(out, Found{Path: p, Rel: rel})
	}
	return out
}

func addedIDs(ps []Project) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.ID)
	}
	sort.Strings(out)
	return out
}

func TestDiscoverKeepsUserChoices(t *testing.T) {
	s, home, log := openTest(t)
	ws := realTemp(t)
	for _, rel := range []string{"a/one", "b/two", "c"} {
		newRepo(t, ws, rel)
	}
	found := foundAt(ws, "a/one", "b/two", "c")
	added, err := s.DiscoverProjects(ws, found)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := addedIDs(added), []string{"a-one", "b-two", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("added = %v, want %v", got, want)
	}
	if ch := log.take(); len(ch) != 3 {
		t.Fatalf("changes = %+v", ch)
	}

	name := "My one"
	off := false
	if _, err := s.UpdateProject("a-one", ProjectUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateProject("b-two", ProjectUpdate{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	log.take()
	before := s.Projects(true)

	again, err := s.DiscoverProjects(ws, found)
	if err != nil || len(again) != 0 {
		t.Fatalf("rediscover = %+v, %v", again, err)
	}
	if ch := log.take(); len(ch) != 0 {
		t.Fatalf("changes on rediscover = %+v", ch)
	}
	if after := s.Projects(true); !reflect.DeepEqual(after, before) {
		t.Fatalf("projects changed:\n%+v\n%+v", before, after)
	}

	// A missing project's folder comes back: not added a second time.
	if err := os.RemoveAll(filepath.Join(ws, "c")); err != nil {
		t.Fatal(err)
	}
	if added, _ := s.DiscoverProjects(ws, foundAt(ws, "a/one", "b/two")); len(added) != 0 {
		t.Fatalf("added = %+v", added)
	}
	if p, _ := s.Project("c"); !p.Missing {
		t.Fatal("c not missing")
	}
	newRepo(t, ws, "c")
	if added, _ := s.DiscoverProjects(ws, found); len(added) != 0 {
		t.Fatalf("returned folder added again: %+v", added)
	}

	// A broken project file whose path can still be read: its folder is
	// known, whether the YAML parses or not.
	newRepo(t, ws, "d")
	newRepo(t, ws, "e")
	writeFile(t, filepath.Join(home, "projects", "hand-d.yaml"), "name: d\npath: "+filepath.Join(ws, "d")+"\nmerge:\n  story: rebase\n")
	writeFile(t, filepath.Join(home, "projects", "hand-e.yaml"), "name: [e\npath: '"+filepath.Join(ws, "e")+"'\n")
	s.Reload()
	if n := len(s.Errors()); n != 2 {
		t.Fatalf("errors = %+v", s.Errors())
	}
	for range 2 {
		if added, err := s.DiscoverProjects(ws, foundAt(ws, "d", "e")); err != nil || len(added) != 0 {
			t.Fatalf("broken file's path added: %+v %v", added, err)
		}
	}

	// The same path twice in one call, and through a link to a known one:
	// added once.
	newRepo(t, ws, "f")
	added, err = s.DiscoverProjects(ws, foundAt(ws, "f", "f"))
	if err != nil || len(added) != 1 {
		t.Fatalf("added = %+v %v", added, err)
	}
	link := filepath.Join(realTemp(t), "link")
	if err := os.Symlink(filepath.Join(ws, "f"), link); err == nil {
		if added, _ := s.DiscoverProjects(ws, []Found{{Path: link, Rel: "link"}}); len(added) != 0 {
			t.Fatalf("link to a known folder added: %+v", added)
		}
	}
}

func TestDiscoverNamesByRelativePath(t *testing.T) {
	s, home, _ := openTest(t)
	ws := realTemp(t)
	repo := newRepo(t, ws, "github.com/acme-corp/ui")
	added, err := s.DiscoverProjects("~/ws", []Found{{Path: repo, Rel: "github.com/acme-corp/ui"}})
	if err != nil || len(added) != 1 {
		t.Fatalf("added = %+v %v", added, err)
	}
	want := Project{ID: "github.com-acme-corp-ui", Name: "github.com/acme-corp/ui", Path: repo, Enabled: true, Root: "~/ws"}
	want = withEffective(want, Defaults().MergeDefaults)
	if added[0] != want {
		t.Fatalf("project = %+v, want %+v", added[0], want)
	}
	if got, ok := s.Project(want.ID); !ok || got != want {
		t.Fatalf("stored = %+v", got)
	}
	// It reads back the same from its file.
	s2, err := Open(home, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s2.Project(want.ID); !ok || got != want {
		t.Fatalf("reloaded = %+v", got)
	}

	// The root itself is named by the root's base name as written.
	rootRepo := newRepo(t, realTemp(t), "whatever")
	added, err = s.DiscoverProjects(abs("/somewhere/my-root"), []Found{{Path: rootRepo, Rel: ""}})
	if err != nil || len(added) != 1 || added[0].Name != "my-root" || added[0].ID != "my-root" {
		t.Fatalf("root project = %+v %v", added, err)
	}

	// Folder names are untrusted: hidden characters and bytes that are not
	// UTF-8 become "-", the name stays one line.
	odd := newRepo(t, ws, "odd")
	added, err = s.DiscoverProjects(ws, []Found{{Path: odd, Rel: "x/evil‮txt.exe\nnext\x1b[31m\xff"}})
	if err != nil || len(added) != 1 {
		t.Fatalf("added = %+v %v", added, err)
	}
	if got := added[0].Name; got != "x/evil-txt.exe-next-[31m-" {
		t.Fatalf("name = %q", got)
	}
}

func TestProjectIDsNeverReused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	var s *Store
	calls := 0
	reserved := func() map[string]bool {
		calls++
		// Called without the store's locks: reading the store must not
		// deadlock.
		_ = s.Projects(true)
		_ = s.General()
		return map[string]bool{"demo": true, "demo-2": true}
	}
	s, err := Open(home, Options{ReservedProjectIDs: reserved})
	if err != nil {
		t.Fatal(err)
	}
	ws := realTemp(t)
	p, err := s.AddProject(newRepo(t, ws, "demo"), "")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "demo-3" {
		t.Fatalf("id = %q, want demo-3", p.ID)
	}
	added, err := s.DiscoverProjects(ws, []Found{{Path: newRepo(t, ws, "other/demo"), Rel: "demo"}})
	if err != nil || len(added) != 1 || added[0].ID != "demo-4" {
		t.Fatalf("added = %+v %v", added, err)
	}
	if calls != 2 {
		t.Fatalf("reserved ids asked %d times, want 2", calls)
	}
	// Without a ticket referencing it, a removed project's id is free
	// again; with nil reserved ids nothing is reserved.
	s2, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p, err := s2.AddProject(newRepo(t, realTemp(t), "demo"), ""); err != nil || p.ID != "demo" {
		t.Fatalf("project = %+v %v", p, err)
	}
}
