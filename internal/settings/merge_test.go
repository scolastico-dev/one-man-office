package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestMergeDefaultsOfAFreshHome(t *testing.T) {
	want := api.MergeModes{Story: api.MergeModeMerge, Task: api.MergeModeMerge, StoryTask: api.MergeModeMerge}
	if got := Defaults().MergeDefaults; got != want {
		t.Fatalf("merge defaults %+v, want %+v", got, want)
	}
	s, home, _ := openTest(t)
	raw, err := os.ReadFile(filepath.Join(home, "settings.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "merge_defaults:\n  story: merge\n  task: merge\n  story_task: merge\n") {
		t.Fatalf("a fresh settings file does not name the merge defaults:\n%s", raw)
	}
	if s.General().MergeDefaults != want {
		t.Fatalf("store merge defaults %+v", s.General().MergeDefaults)
	}
}

// A settings file without merge_defaults, or naming only some, gets the
// defaults for the keys it leaves out.
func TestMergeDefaultsLeftOutOfAFile(t *testing.T) {
	s, home, _ := openTest(t)
	path := filepath.Join(home, "settings.yaml")
	writeFile(t, path, "max_running_agents: 3\n")
	s.Reload()
	if g := s.General(); g.MergeDefaults != Defaults().MergeDefaults {
		t.Fatalf("merge defaults %+v", g.MergeDefaults)
	}
	writeFile(t, path, "merge_defaults:\n  task: squash\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 0 {
		t.Fatalf("errors %+v", errs)
	}
	want := api.MergeModes{Story: api.MergeModeMerge, Task: api.MergeModeSquash, StoryTask: api.MergeModeMerge}
	if g := s.General(); g.MergeDefaults != want {
		t.Fatalf("merge defaults %+v, want %+v", g.MergeDefaults, want)
	}
	if g := LoadGeneral(path); g.MergeDefaults != want {
		t.Fatalf("LoadGeneral merge defaults %+v", g.MergeDefaults)
	}
}

func TestMergeDefaultsAreValidated(t *testing.T) {
	s, _, _ := openTest(t)
	for field, mutate := range map[string]func(*General){
		"merge_defaults.story":      func(g *General) { g.MergeDefaults.Story = "rebase" },
		"merge_defaults.task":       func(g *General) { g.MergeDefaults.Task = "" },
		"merge_defaults.story_task": func(g *General) { g.MergeDefaults.StoryTask = "fast-forward" },
	} {
		g := Defaults()
		mutate(&g)
		err := s.SetGeneral(g)
		var ae *api.Error
		if !asAPIError(err, &ae) || ae.Code != api.CodeInvalidSettings || !strings.Contains(ae.Message, field) {
			t.Errorf("%s: err = %v", field, err)
			continue
		}
		if fd, _ := ae.Details.(api.FieldDetail); fd.Field != field {
			t.Errorf("%s: err = %v", field, err)
		}
	}
}

// A project follows the general defaults until it sets a mode of its own;
// "" goes back to the default.
func TestProjectMergeModesOverrideTheDefaults(t *testing.T) {
	s, _, _ := openTest(t)
	p, err := s.AddProject(newRepo(t, t.TempDir(), "r"), "")
	if err != nil {
		t.Fatal(err)
	}
	merge := Defaults().MergeDefaults
	if p.Merge != (api.MergeModes{}) || p.EffectiveMerge != merge {
		t.Fatalf("new project merge %+v effective %+v", p.Merge, p.EffectiveMerge)
	}
	g := s.General()
	g.MergeDefaults.Task = api.MergeModeSquash
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Project(p.ID)
	if got.Merge.Task != "" || got.EffectiveMerge.Task != api.MergeModeSquash {
		t.Fatalf("after the default changed: merge %+v effective %+v", got.Merge, got.EffectiveMerge)
	}
	squash, mergeMode := api.MergeModeSquash, api.MergeModeMerge
	got, err = s.UpdateProject(p.ID, ProjectUpdate{MergeStory: &squash, MergeTask: &mergeMode})
	if err != nil {
		t.Fatal(err)
	}
	want := api.MergeModes{Story: squash, Task: mergeMode}
	if got.Merge != want || got.EffectiveMerge != (api.MergeModes{Story: squash, Task: mergeMode, StoryTask: api.MergeModeMerge}) || got.StoryMerge != squash {
		t.Fatalf("after the update: %+v", got)
	}
	if list := s.Projects(true); len(list) != 1 || list[0].EffectiveMerge != got.EffectiveMerge {
		t.Fatalf("listed %+v", list)
	}
	for _, c := range []struct {
		story, inStory bool
		want           string
	}{{true, false, squash}, {false, false, mergeMode}, {false, true, api.MergeModeMerge}} {
		typ := api.TypeTask
		if c.story {
			typ = api.TypeStory
		}
		if m := MergeModeFor(got, typ, c.inStory); m != c.want {
			t.Errorf("MergeModeFor(%s, in story %v) = %s, want %s", typ, c.inStory, m, c.want)
		}
	}
	empty := ""
	got, err = s.UpdateProject(p.ID, ProjectUpdate{MergeTask: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if got.Merge.Task != "" || got.EffectiveMerge.Task != api.MergeModeSquash || got.Merge.Story != squash {
		t.Fatalf("after going back to the default: %+v", got)
	}
}

// Project files of older versions name story_merge: squash keeps its
// meaning as the project's own mode for stories, merge (written into every
// file as the default) leaves the project on the general default. The next
// write stores the new key only.
func TestOlderStoryMergeKeyIsRead(t *testing.T) {
	s, home, _ := openTest(t)
	repo := newRepo(t, t.TempDir(), "r")
	dir := filepath.Join(home, "projects")
	writeFile(t, filepath.Join(dir, "sq.yaml"), "name: sq\npath: "+repo+"\nenabled: true\nroot: \"\"\nstory_merge: squash\n")
	writeFile(t, filepath.Join(dir, "mc.yaml"), "name: mc\npath: "+newRepo(t, t.TempDir(), "m")+"\nenabled: true\nroot: \"\"\nstory_merge: merge\n")
	writeFile(t, filepath.Join(dir, "both.yaml"), "name: both\npath: "+newRepo(t, t.TempDir(), "b")+"\nstory_merge: squash\nmerge:\n  story: merge\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 0 {
		t.Fatalf("errors %+v", errs)
	}
	g := s.General()
	g.MergeDefaults.Story = api.MergeModeSquash
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	sq, _ := s.Project("sq")
	if sq.Merge.Story != api.MergeModeSquash || sq.EffectiveMerge.Story != api.MergeModeSquash {
		t.Fatalf("story_merge: squash read as %+v / %+v", sq.Merge, sq.EffectiveMerge)
	}
	mc, _ := s.Project("mc")
	if mc.Merge != (api.MergeModes{}) || mc.EffectiveMerge.Story != api.MergeModeSquash {
		t.Fatalf("story_merge: merge read as %+v / %+v", mc.Merge, mc.EffectiveMerge)
	}
	both, _ := s.Project("both")
	if both.Merge.Story != api.MergeModeMerge {
		t.Fatalf("merge.story did not win over story_merge: %+v", both.Merge)
	}
	name := "renamed"
	if _, err := s.UpdateProject("sq", ProjectUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sq.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "story_merge") || !strings.Contains(string(raw), "merge:\n  story: squash\n") {
		t.Fatalf("written file:\n%s", raw)
	}
	if _, err := s.UpdateProject("mc", ProjectUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, "mc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "merge") {
		t.Fatalf("a project on the defaults names merge modes:\n%s", raw)
	}
}

func TestProjectMergeModesAreValidated(t *testing.T) {
	s, home, _ := openTest(t)
	repo := newRepo(t, t.TempDir(), "r")
	for field, content := range map[string]string{
		"merge.story":      "merge:\n  story: rebase\n",
		"merge.task":       "merge:\n  task: fast-forward\n",
		"merge.story_task": "merge:\n  story_task: x\n",
		"story_merge":      "story_merge: rebase\n",
	} {
		path := filepath.Join(home, "projects", "p.yaml")
		writeFile(t, path, "name: p\npath: "+repo+"\n"+content)
		s.Reload()
		errs := s.Errors()
		if len(errs) != 1 || !strings.Contains(errs[0].Message, field) {
			t.Errorf("%s: errors %+v", field, errs)
		}
		os.Remove(path)
		s.Reload()
	}
}
