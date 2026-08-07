package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDeveloperMandatesSuperpowers(t *testing.T) {
	out, err := Render(t.TempDir(), "developer", Data{Name: "developer-jason", Role: "developer", Goal: "build /health", JobID: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"developer-jason", "build /health",
		"executing-plans", "test-driven-development", "verification-before-completion",
		"omo inbox", "omo done", "omo wait", "omo step", "omo agent list", "Never invoke subagents",
		"plain command beginning with `omo`", "act as a transparent", "then resume what you were",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("developer prompt missing %q", want)
		}
	}
}

func TestRenderAllRoles(t *testing.T) {
	for _, role := range []string{"ceo", "product_manager", "developer", "reviewer", "freelancer", "smokealarm", "firefighter"} {
		if _, err := Render(t.TempDir(), role, Data{Name: "x", Role: role, Goal: "g"}); err != nil {
			t.Errorf("render %s: %v", role, err)
		}
	}
}

func TestOfficeOverrideWins(t *testing.T) {
	office := t.TempDir()
	os.MkdirAll(filepath.Join(office, Dir), 0o755)
	os.WriteFile(filepath.Join(office, Dir, "developer.md"), []byte("CUSTOM {{.Goal}}"), 0o644)
	out, err := Render(office, "developer", Data{Goal: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "CUSTOM g1") {
		t.Fatalf("override not applied:\n%s", out)
	}
}

func TestUnknownRoleErrors(t *testing.T) {
	if _, err := Render(t.TempDir(), "janitor", Data{}); err == nil {
		t.Fatal("expected error")
	}
}
