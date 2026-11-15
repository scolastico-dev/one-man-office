package settings

import (
	"os"
	"path/filepath"
	"reflect"
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
	if err := s.PutPrompt("developer", "mine\n"); err != nil {
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
	if err := s.ResetPrompt("developer"); err != nil {
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
	if err := s.ResetPrompt("developer"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.ResetPrompt("ghost"), api.CodeNotFound)
	wantCode(t, s.PutPrompt("Bad Name", "x"), api.CodeValidation)
	wantCode(t, s.PutPrompt("developer", "  \n"), api.CodeValidation)
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
	s, home, _ := openWithPrompts(t, nil)
	if err := s.PutPrompt("x", "good"); err != nil {
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
