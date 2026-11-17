package settings

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func openWithPrompts(t *testing.T, defaults map[string]string) (*Store, string, *changeLog) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	log := &changeLog{}
	s, err := Open(home, Options{PromptDefaults: defaults, OnChange: log.add})
	if err != nil {
		t.Fatal(err)
	}
	return s, home, log
}

func TestPromptOverrideAndReset(t *testing.T) {
	s, home, log := openWithPrompts(t, map[string]string{"developer": "D"})
	text, custom, ok := s.Prompt("developer")
	if text != "D" || custom || !ok {
		t.Fatalf("Prompt = %q %v %v", text, custom, ok)
	}
	if err := s.PutPromptIf("", "developer", "mine\n"); err != nil {
		t.Fatal(err)
	}
	text, custom, ok = s.Prompt("developer")
	if text != "mine\n" || !custom || !ok {
		t.Fatalf("Prompt = %q %v %v", text, custom, ok)
	}
	path := filepath.Join(home, "prompts", "developer.md")
	if b, err := os.ReadFile(path); err != nil || string(b) != "mine\n" {
		t.Fatalf("file = %q, %v", b, err)
	}
	if err := s.ResetPromptIf("", "developer"); err != nil {
		t.Fatal(err)
	}
	text, custom, _ = s.Prompt("developer")
	if text != "D" || custom {
		t.Fatalf("Prompt = %q %v", text, custom)
	}
	if fileExists(path) {
		t.Fatal("override file remains")
	}
	c := log.take()
	if len(c) != 2 || c[0].Section != SectionPrompts || c[0].Removed || !c[1].Removed || c[1].Name != "developer" {
		t.Fatalf("changes = %+v", c)
	}
	// Resetting what is not customized is a no-op; unknown prompts are not found.
	if err := s.ResetPromptIf("", "developer"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.ResetPromptIf("", "ghost"), api.CodeNotFound)
	wantCode(t, s.PutPromptIf("", "Bad Name", "x"), api.CodeNotFound)
	wantCode(t, s.PutPromptIf("", "developer", "  \n"), api.CodeValidation)
}

func TestPromptsListUnionOfDefaultsAndFiles(t *testing.T) {
	s, home, _ := openWithPrompts(t, map[string]string{"developer": "D", "pm": "P"})
	writeFile(t, filepath.Join(home, "prompts", "pm.md"), "custom pm")
	writeFile(t, filepath.Join(home, "prompts", "extra.md"), "x")
	s.Reload()
	want := []PromptInfo{{Name: "developer"}, {Name: "extra", Customized: true}, {Name: "pm", Customized: true}}
	if got := s.Prompts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Prompts = %+v, want %+v", got, want)
	}
	if text, custom, _ := s.Prompt("pm"); text != "custom pm" || !custom {
		t.Fatalf("pm = %q %v", text, custom)
	}
	// Deleting the file by hand falls back to the default.
	if err := os.Remove(filepath.Join(home, "prompts", "pm.md")); err != nil {
		t.Fatal(err)
	}
	s.Reload()
	if text, custom, _ := s.Prompt("pm"); text != "P" || custom {
		t.Fatalf("pm = %q %v", text, custom)
	}
}

func TestInvalidPromptFileKeepsLastValid(t *testing.T) {
	s, home, _ := openWithPrompts(t, map[string]string{"x": "default"})
	if err := s.PutPromptIf("", "x", "good"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "prompts", "x.md"), "bad \xff\xfe")
	s.Reload()
	if text, _, _ := s.Prompt("x"); text != "good" {
		t.Fatalf("text = %q, want last valid", text)
	}
	if len(s.Errors()) != 1 {
		t.Fatalf("errors = %+v", s.Errors())
	}
}

func TestPutPromptUnknownName(t *testing.T) {
	s, home, log := openWithPrompts(t, map[string]string{"developer": "D"})
	wantCode(t, s.PutPromptIf("", "ghost", "x"), api.CodeNotFound)
	wantCode(t, s.PutPromptIf(NoRevision, "ghost", "x"), api.CodeNotFound)
	wantCode(t, s.ResetPromptIf("", "ghost"), api.CodeNotFound)
	wantCode(t, s.PutPromptIf("", "../developer", "x"), api.CodeNotFound)
	// A file without a default is not one omo reads: it cannot be reset
	// through the store either.
	writeFile(t, filepath.Join(home, "prompts", "extra.md"), "x")
	s.Reload()
	log.take()
	wantCode(t, s.ResetPromptIf("", "extra"), api.CodeNotFound)
	if fileExists(filepath.Join(home, "prompts", "ghost.md")) || len(log.take()) != 0 {
		t.Fatal("an unknown prompt was written")
	}
}

func TestPutPromptRefusesBrokenTemplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	var seen []string
	s, err := Open(home, Options{
		PromptDefaults: map[string]string{"developer": "D"},
		ValidatePrompt: func(name, text string) error {
			seen = append(seen, name)
			if strings.Contains(text, "{{") {
				return errors.New(`template: developer:1: unexpected "}" in operand`)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.PutPromptIf("", "developer", "{{ } }")
	wantCode(t, err, api.CodeValidation)
	if fieldOf(err) != "text" || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("err = %v, field %q", err, fieldOf(err))
	}
	if fileExists(filepath.Join(home, "prompts", "developer.md")) {
		t.Fatal("broken template written")
	}
	if err := s.PutPromptIf("", "developer", "fine"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []string{"developer", "developer"}) {
		t.Fatalf("ValidatePrompt calls = %v", seen)
	}
	// Size bound.
	err = s.PutPromptIf("", "developer", strings.Repeat("x", 256<<10+1))
	wantCode(t, err, api.CodeValidation)
	if fieldOf(err) != "text" {
		t.Fatalf("field = %q", fieldOf(err))
	}
	if err := s.PutPromptIf("", "developer", strings.Repeat("x", 256<<10)); err != nil {
		t.Fatal(err)
	}
}

func TestBlankInstructionsResets(t *testing.T) {
	s, home, log := openWithPrompts(t, map[string]string{"developer": "D", "developer-instructions": ""})
	if err := s.PutPromptIf("", "developer-instructions", "Write tests.\n"); err != nil {
		t.Fatal(err)
	}
	rev := s.Revision(SectionPrompts, "developer-instructions")
	log.take()
	if err := s.PutPromptIf(rev, "developer-instructions", " \n"); err != nil {
		t.Fatal(err)
	}
	if text, custom, _ := s.Prompt("developer-instructions"); custom || text != "" {
		t.Fatalf("after blank = %q %v", text, custom)
	}
	if fileExists(filepath.Join(home, "prompts", "developer-instructions.md")) {
		t.Fatal("file remains")
	}
	if ch := log.take(); len(ch) != 1 || !ch[0].Removed {
		t.Fatalf("changes = %+v", ch)
	}
	if got := s.Revision(SectionPrompts, "developer-instructions"); got != NoRevision {
		t.Fatalf("revision = %q", got)
	}
	// Blank again: nothing to do. A blank role prompt is still refused.
	if err := s.PutPromptIf("", "developer-instructions", ""); err != nil {
		t.Fatal(err)
	}
	err := s.PutPromptIf("", "developer", " ")
	wantCode(t, err, api.CodeValidation)
	if fieldOf(err) != "text" {
		t.Fatalf("field = %q", fieldOf(err))
	}
}
