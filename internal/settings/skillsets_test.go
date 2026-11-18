package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func superpowers() SkillSet {
	return SkillSet{
		Name:         "superpowers",
		Repository:   "https://github.com/obra/superpowers.git",
		Ref:          "main",
		Update:       api.SkillSetAtStart,
		Enabled:      true,
		Paths:        []string{},
		Roles:        []string{},
		Instructions: map[string]string{"developer": "Read SKILLS.md first.\n", "reviewer": "Check\tthe skills.\r\n"},
	}
}

func TestSkillSetValidation(t *testing.T) {
	s, home, _ := openTest(t)
	cases := map[string]func(*SkillSet){
		"name":                  func(k *SkillSet) { k.Name = "Bad Name" },
		"repository":            func(k *SkillSet) { k.Repository = "ext::sh -c touch% /tmp/x" },
		"repository ":           func(k *SkillSet) { k.Repository = "" },
		"repository  ":          func(k *SkillSet) { k.Repository = "http://example.com/x.git" },
		"repository   ":         func(k *SkillSet) { k.Repository = "https://example.com/" + strings.Repeat("x", 3000) },
		"ref":                   func(k *SkillSet) { k.Ref = "-x" },
		"ref ":                  func(k *SkillSet) { k.Ref = "a..b" },
		"update":                func(k *SkillSet) { k.Update = "daily" },
		"instructions":          func(k *SkillSet) { k.Instructions = map[string]string{"tester": "x"} },
		"instructions.pm":       func(k *SkillSet) { k.Instructions = map[string]string{"pm": strings.Repeat("x", 17<<10)} },
		"instructions.ceo":      func(k *SkillSet) { k.Instructions = map[string]string{"ceo": "a‮b"} },
		"instructions.export":   func(k *SkillSet) { k.Instructions = map[string]string{"export": "\xff"} },
		"instructions.reviewer": func(k *SkillSet) { k.Instructions = map[string]string{"reviewer": "a\rb"} },
		"instructions.developer": func(k *SkillSet) {
			k.Roles = []string{"reviewer"}
		},
		"roles.1":           func(k *SkillSet) { k.Roles = []string{"developer", "tester"} },
		"roles.2":           func(k *SkillSet) { k.Roles = []string{"developer", "reviewer", "developer"} },
		"paths":             func(k *SkillSet) { k.Paths = make([]string, 65) },
		"paths.0":           func(k *SkillSet) { k.Paths = []string{""} },
		"paths.1":           func(k *SkillSet) { k.Paths = []string{"skills", "/etc/passwd"} },
		"paths.0 ":          func(k *SkillSet) { k.Paths = []string{"../outside"} },
		"paths.0  ":         func(k *SkillSet) { k.Paths = []string{"skills/../../outside"} },
		"paths.0   ":        func(k *SkillSet) { k.Paths = []string{"skills/./tdd"} },
		"paths.0    ":       func(k *SkillSet) { k.Paths = []string{"skills\\tdd"} },
		"paths.0     ":      func(k *SkillSet) { k.Paths = []string{"C:/skills"} },
		"paths.0      ":     func(k *SkillSet) { k.Paths = []string{"."} },
		"paths.0       ":    func(k *SkillSet) { k.Paths = []string{"skills/"} },
		"paths.0        ":   func(k *SkillSet) { k.Paths = []string{"a\u202eb"} },
		"paths.0         ":  func(k *SkillSet) { k.Paths = []string{".git/config"} },
		"paths.0          ": func(k *SkillSet) { k.Paths = []string{strings.Repeat("x", 513)} },
		"paths.1 ":          func(k *SkillSet) { k.Paths = []string{"skills/tdd", "skills/tdd"} },
	}
	for name, mutate := range cases {
		k := superpowers()
		mutate(&k)
		err := s.PutSkillSetIf("", k)
		wantCode(t, err, api.CodeValidation)
		if got := fieldOf(err); got != strings.TrimSpace(name) {
			t.Errorf("%q: field %q, err %v", name, got, err)
		}
		if strings.Contains(err.Error(), "touch") || strings.Contains(err.Error(), "xxxx") {
			t.Errorf("%q: message quotes the value: %v", name, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "skillsets")); len(entries) != 0 {
		t.Fatalf("refused skill sets were written: %v", entries)
	}
	// Roles and paths: a text only for a role that gets the set; paths
	// relative to the repository's root, folders and files alike.
	k := superpowers()
	k.Roles = []string{"reviewer", "developer"}
	k.Paths = []string{"skills/test-driven-development", "skills/brainstorming/SKILL.md", "README.md"}
	if err := s.PutSkillSetIf("", k); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SkillSet("superpowers"); !reflect.DeepEqual(got.Roles, k.Roles) || !reflect.DeepEqual(got.Paths, k.Paths) {
		t.Fatalf("skill set = %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "skillsets", "superpowers.yaml"))
	if !strings.Contains(string(raw), "roles:\n  - reviewer\n  - developer\n") || !strings.Contains(string(raw), "paths:\n  - skills/test-driven-development\n") {
		t.Fatalf("file:\n%s", raw)
	}
	if err := s.DeleteSkillSetIf("", "superpowers"); err != nil {
		t.Fatal(err)
	}
	// Accepted addresses and refs.
	for _, repo := range []string{"git@github.com:obra/superpowers.git", "ssh://git@host/x.git", abs("/srv/git/skills")} {
		k := superpowers()
		k.Repository = repo
		k.Ref = ""
		k.Update = ""
		if err := s.PutSkillSetIf("", k); err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		got, _ := s.SkillSet("superpowers")
		if got.Repository != repo || got.Update != api.SkillSetAtStart {
			t.Fatalf("skill set = %+v", got)
		}
	}
	// A hand-written file is validated the same way.
	writeFile(t, filepath.Join(home, "skillsets", "hand.yaml"), "repository: ext::sh\nupdate: pinned\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 1 || !strings.Contains(errs[0].Message, "repository") || strings.Contains(errs[0].Message, "ext::") {
		t.Fatalf("errors = %+v", errs)
	}
	if _, ok := s.SkillSet("hand"); ok {
		t.Fatal("broken skill set loaded")
	}
}

func TestSkillSetRevisions(t *testing.T) {
	s, home, log := openTest(t)
	k := superpowers()
	if err := s.PutSkillSetIf(NoRevision, k); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.PutSkillSetIf(NoRevision, k), api.CodeStaleSettings)
	got, ok := s.SkillSet("superpowers")
	rev := s.Revision(SectionSkillSets, "superpowers")
	if !ok || got.Revision != rev || rev == NoRevision {
		t.Fatalf("skill set = %+v, revision %q", got, rev)
	}
	got.Revision = ""
	if !reflect.DeepEqual(got, k) {
		t.Fatalf("skill set = %+v, want %+v", got, k)
	}
	if list := s.SkillSets(); len(list) != 1 || list[0].Revision != rev {
		t.Fatalf("list = %+v", list)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "skillsets", "superpowers.yaml"))
	if strings.Contains(string(raw), "revision") || strings.Contains(string(raw), "name:") {
		t.Fatalf("file holds name or revision:\n%s", raw)
	}
	// A hand edit not yet polled makes the page's revision stale; the edit
	// is delivered as from disk.
	log.take()
	writeFile(t, filepath.Join(home, "skillsets", "superpowers.yaml"), "repository: "+abs("/srv/skills")+"\nenabled: false\n")
	k.Enabled = false
	wantCode(t, s.PutSkillSetIf(rev, k), api.CodeStaleSettings)
	ch := log.take()
	if len(ch) != 1 || ch[0].Section != SectionSkillSets || !ch[0].FromDisk {
		t.Fatalf("changes = %+v", ch)
	}
	if got, _ := s.SkillSet("superpowers"); got.Repository != abs("/srv/skills") {
		t.Fatalf("hand edit not loaded: %+v", got)
	}
	wantCode(t, s.DeleteSkillSetIf(rev, "superpowers"), api.CodeStaleSettings)
	if err := s.DeleteSkillSetIf(s.Revision(SectionSkillSets, "superpowers"), "superpowers"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.SkillSet("superpowers"); ok || fileExists(filepath.Join(home, "skillsets", "superpowers.yaml")) {
		t.Fatal("skill set not deleted")
	}
	wantCode(t, s.DeleteSkillSetIf("", "superpowers"), api.CodeNotFound)
	if ch := log.take(); len(ch) != 1 || !ch[0].Removed || ch[0].FromDisk {
		t.Fatalf("changes = %+v", ch)
	}
	// A broken file can be deleted.
	writeFile(t, filepath.Join(home, "skillsets", "broken.yaml"), "repository: [")
	s.Reload()
	if err := s.DeleteSkillSetIf("", "broken"); err != nil {
		t.Fatal(err)
	}
	if len(s.Errors()) != 0 {
		t.Fatalf("errors = %+v", s.Errors())
	}
}
