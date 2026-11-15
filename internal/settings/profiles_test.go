package settings

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func boolPtr(b bool) *bool { return &b }

func claudeProfile() Profile {
	return Profile{
		Name:    "claude",
		CLI:     api.CLIClaude,
		Command: "claude",
		Args:    []string{"--dangerously-skip-permissions"},
		Models: map[string]Model{
			"fable": {Args: []string{"--model", "fable"}, Selectable: boolPtr(true), Description: "Strongest reasoning"},
			"opus":  {Args: []string{"--model", "opus"}, Selectable: boolPtr(false)},
		},
	}
}

func TestModelsFlattenProfiles(t *testing.T) {
	s, _, log := openTest(t)
	if err := s.PutProfile(claudeProfile()); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range s.Models() {
		ids = append(ids, m.ID)
	}
	if want := []string{"claude/fable", "claude/opus"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	opus, ok := s.Model("claude/opus")
	if !ok || opus.Selectable || opus.Profile != "claude" || opus.Name != "opus" || opus.CLI != "claude" {
		t.Fatalf("opus = %+v, %v", opus, ok)
	}
	fable, _ := s.Model("claude/fable")
	if !fable.Selectable || fable.Description != "Strongest reasoning" {
		t.Fatalf("fable = %+v", fable)
	}
	if _, ok := s.Model("claude/nope"); ok {
		t.Fatal("unknown model found")
	}
	if _, ok := s.Model("claude"); ok {
		t.Fatal("bare profile name found as a model")
	}
	if c := log.take(); len(c) != 1 || c[0].Section != SectionProfiles || c[0].Name != "claude" {
		t.Fatalf("changes = %+v", c)
	}
	// A model that leaves selectable out is selectable.
	p := claudeProfile()
	p.Models = map[string]Model{"plain": {}}
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Model("claude/plain"); !m.Selectable {
		t.Fatal("nil selectable must mean selectable")
	}
}

func TestProfileNeedsAModel(t *testing.T) {
	s, _, _ := openTest(t)
	p := claudeProfile()
	p.Models = nil
	wantCode(t, s.PutProfile(p), api.CodeValidation)
	p.Models = map[string]Model{}
	wantCode(t, s.PutProfile(p), api.CodeValidation)
	p = claudeProfile()
	p.CLI = "vim"
	wantCode(t, s.PutProfile(p), api.CodeValidation)
	p = claudeProfile()
	p.Name = "Bad Name"
	wantCode(t, s.PutProfile(p), api.CodeValidation)
	p = claudeProfile()
	p.Models = map[string]Model{"a/b": {}}
	wantCode(t, s.PutProfile(p), api.CodeValidation)
	if len(s.Profiles()) != 0 {
		t.Fatal("an invalid profile was stored")
	}
}

func TestProfileRoundTripsThroughItsFile(t *testing.T) {
	s, home, _ := openTest(t)
	p := claudeProfile()
	d := api.Duration(3 * time.Second)
	n := 3
	p.PromptDelivery = PromptDelivery{Inject: boolPtr(true), Delay: &d, Retries: &n}
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	// A reload that reads the written file gives the same profile.
	s2, err := Open(home, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Profile("claude")
	if !ok || got.CLI != "claude" || got.Command != "claude" || *got.PromptDelivery.Delay != d || *got.PromptDelivery.Retries != 3 || got.PromptDelivery.RetryWait != nil {
		t.Fatalf("profile = %+v, %v", got, ok)
	}
	if got.Models["fable"].Args[1] != "fable" || got.Env == nil || got.Models["opus"].Env == nil {
		t.Fatalf("models = %+v env = %v", got.Models, got.Env)
	}
	if len(s2.Errors()) != 0 {
		t.Fatalf("errors = %+v", s2.Errors())
	}
	// Callers cannot change the store through what they get back.
	got.Models["fable"].Args[0] = "changed"
	if again, _ := s.Profile("claude"); again.Models["fable"].Args[0] == "changed" {
		t.Fatal("Profile returned shared state")
	}
}

func TestMalformedProfileHandEditKeepsLastValid(t *testing.T) {
	s, home, log := openTest(t)
	if err := s.PutProfile(claudeProfile()); err != nil {
		t.Fatal(err)
	}
	log.take()
	path := filepath.Join(home, "profiles", "claude.yaml")
	writeFile(t, path, "cli: claude\ncommand: claude\nmodels:\n  fable: {}\nbogus_key: 1\n")
	s.Reload()
	errs := s.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "bogus_key") {
		t.Fatalf("errors = %+v", errs)
	}
	if _, ok := s.Model("claude/opus"); !ok {
		t.Fatal("last valid profile no longer in effect")
	}
	if c := log.take(); len(c) != 1 || c[0].Section != SectionProfiles || c[0].Err == nil {
		t.Fatalf("changes = %+v", c)
	}
	// A profile without models in a hand-written file is an error too.
	writeFile(t, path, "cli: claude\ncommand: claude\nmodels: {}\n")
	s.Reload()
	if len(s.Errors()) != 1 {
		t.Fatalf("errors = %+v", s.Errors())
	}
	if _, ok := s.Model("claude/opus"); !ok {
		t.Fatal("last valid profile no longer in effect")
	}
	// A new hand-written profile is picked up.
	writeFile(t, filepath.Join(home, "profiles", "codex.yaml"), "cli: codex\ncommand: codex\nmodels:\n  astra: {description: x}\n")
	s.Reload()
	if _, ok := s.Model("codex/astra"); !ok {
		t.Fatal("hand-added profile not loaded")
	}
}

func TestDeleteProfileInUse(t *testing.T) {
	s, home, _ := openTest(t)
	if err := s.PutProfile(claudeProfile()); err != nil {
		t.Fatal(err)
	}
	if err := s.PutRole(Role{Name: "developer", Models: []string{"claude/fable"}, Assignment: api.AssignmentFailover}); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteProfile("claude")
	wantCode(t, err, api.CodeInUse)
	if !strings.Contains(err.Error(), "developer") {
		t.Fatalf("error %q does not name the role", err)
	}
	if _, ok := s.Profile("claude"); !ok {
		t.Fatal("profile deleted despite being in use")
	}
	if err := s.PutRole(Role{Name: "developer"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProfile("claude"); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(home, "profiles", "claude.yaml")) {
		t.Fatal("profile file remains")
	}
	wantCode(t, s.DeleteProfile("claude"), api.CodeNotFound)
}
