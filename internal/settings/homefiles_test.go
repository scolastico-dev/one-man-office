package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// A settings file replaced by something that is not a plain file of a
// sensible size is reported broken with a fixed message; the last good
// content stays in effect; neither the poll nor the re-read before a write
// waits on it.

// replacement puts something other than a plain settings file at path.
type replacement struct {
	name string
	// put replaces the file at path; limit is the store's limit for it.
	put func(t *testing.T, path string, limit int64)
	// message is the error the store reports for it.
	message func(limit int64) string
}

func refusalOf(err error, limit int64) string {
	msg, _ := homefile.Refusal(err, limit)
	return msg
}

var portableReplacements = []replacement{
	{
		name: "a folder",
		put: func(t *testing.T, path string, _ int64) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		message: func(l int64) string { return refusalOf(homefile.ErrNotRegular, l) },
	},
	{
		name: "an oversized file",
		put: func(t *testing.T, path string, limit int64) {
			// Valid content padded with a comment: only the size is wrong.
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			pad := "\n#" + strings.Repeat("x", int(limit)) + "\n"
			if err := os.WriteFile(path, append(data, pad...), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		message: func(l int64) string { return refusalOf(homefile.ErrTooLarge, l) },
	},
}

// within fails when f does not return within five seconds.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocked", what)
	}
}

// errorOf is the message the store reports for path.
func errorOf(s *Store, path string) string {
	for _, e := range s.Errors() {
		if e.File == path {
			return e.Message
		}
	}
	return ""
}

// checkReplacements runs every replacement against the settings, a profile, a
// role, a prompt and a project file, for the poll and for the re-read before
// a write.
func checkReplacements(t *testing.T, reps []replacement) {
	for _, r := range reps {
		t.Run(r.name, func(t *testing.T) {
			t.Run("poll", func(t *testing.T) { checkPoll(t, r) })
			t.Run("write", func(t *testing.T) { checkGuard(t, r) })
		})
	}
}

func checkPoll(t *testing.T, r replacement) {
	s, home, _ := openTest(t)
	g := s.General()
	g.Limits.Tasks = 7
	if err := s.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	if err := s.PutProfileIf("", fakeProfile("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutRoleIf("", Role{Name: "developer", Models: []string{"x/a"}}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "prompts", "developer.md"), "be careful\n")
	if err := s.PutUsageScriptIf("", "fake", "function usage() end\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSkillSetIf("", superpowers()); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPluginIf("", pushover()); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	pr, err := s.AddProject(newRepo(t, ws, "ui"), "ui")
	if err != nil {
		t.Fatal(err)
	}
	s.Reload()
	if errs := s.Errors(); len(errs) != 0 {
		t.Fatalf("errors before: %+v", errs)
	}

	files := map[string]int64{
		filepath.Join(home, "settings.yaml"):                 homefile.MaxSettingsFile,
		filepath.Join(home, "profiles", "x.yaml"):            homefile.MaxSettingsFile,
		filepath.Join(home, "roles", "developer.yaml"):       homefile.MaxSettingsFile,
		filepath.Join(home, "prompts", "developer.md"):       homefile.MaxPromptFile,
		filepath.Join(home, "projects", pr.ID+".yaml"):       homefile.MaxSettingsFile,
		filepath.Join(home, "usage", "fake.lua"):             maxUsageScript,
		filepath.Join(home, "skillsets", "superpowers.yaml"): homefile.MaxSettingsFile,
	}
	// A folder in the plugins folder is a plugin's tree, not a broken
	// settings file (TestPluginFolderSkipsTrees).
	if r.name != "a folder" {
		files[filepath.Join(home, "plugins", "pushover.yaml")] = homefile.MaxSettingsFile
	}
	for path, limit := range files {
		r.put(t, path, limit)
	}
	within(t, "the poll", func() { s.Reload() })
	for path, limit := range files {
		if got, want := errorOf(s, path), r.message(limit); got != want {
			t.Errorf("%s: error %q, want %q", filepath.Base(path), got, want)
		}
	}
	// The last good content stays in effect.
	if s.General().Limits.Tasks != 7 {
		t.Error("the settings were dropped")
	}
	if _, ok := s.Profile("x"); !ok {
		t.Error("the profile was dropped")
	}
	if role, _ := s.Role("developer"); len(role.Models) != 1 {
		t.Error("the role was dropped")
	}
	if text, customized, _ := s.Prompt("developer"); !customized || text != "be careful\n" {
		t.Error("the prompt was dropped")
	}
	if len(s.Projects(true)) != 1 {
		t.Error("the project was dropped")
	}
	if _, custom, _ := s.UsageScript("fake"); !custom {
		t.Error("the usage script was dropped")
	}
	if _, ok := s.SkillSet("superpowers"); !ok {
		t.Error("the skill set was dropped")
	}
	// A second poll reports nothing new; finding the paths of broken
	// project files does not wait either.
	within(t, "the second poll", func() {
		if ch := s.Reload(); len(ch) != 0 {
			t.Errorf("second poll: %+v", ch)
		}
	})
	within(t, "discovery", func() {
		if _, err := s.DiscoverProjects(ws, []Found{{Path: newRepo(t, ws, "api"), Rel: "api"}}); err != nil {
			t.Error(err)
		}
	})
}

func checkGuard(t *testing.T, r replacement) {
	s, home, _ := openTest(t)
	if err := s.PutProfileIf("", fakeProfile("x")); err != nil {
		t.Fatal(err)
	}
	g := s.General()
	g.Limits.Tasks = 7
	if err := s.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	profileRev := s.Revision(SectionProfiles, "x")
	generalRev := s.Revision(SectionGeneral, "")
	profile := filepath.Join(home, "profiles", "x.yaml")
	general := filepath.Join(home, "settings.yaml")
	r.put(t, profile, homefile.MaxSettingsFile)
	r.put(t, general, homefile.MaxSettingsFile)

	// The write re-reads the file, finds it broken and refuses the
	// revision the page had.
	var perr, gerr error
	within(t, "the profile write", func() { perr = s.PutProfileIf(profileRev, fakeProfile("x")) })
	within(t, "the settings write", func() {
		_, gerr = s.UpdateGeneral(generalRev, func(g *General) error { g.Limits.Tasks = 3; return nil })
	})
	wantCode(t, perr, api.CodeStaleSettings)
	wantCode(t, gerr, api.CodeStaleSettings)
	want := r.message(homefile.MaxSettingsFile)
	if got := errorOf(s, profile); got != want {
		t.Errorf("profile error %q, want %q", got, want)
	}
	if got := errorOf(s, general); got != want {
		t.Errorf("settings error %q, want %q", got, want)
	}
	if _, ok := s.Profile("x"); !ok || s.General().Limits.Tasks != 7 {
		t.Error("the last good content was dropped")
	}
}

func TestSettingsFilesThatAreNotPlainFiles(t *testing.T) {
	checkReplacements(t, portableReplacements)
}
