package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDeveloperMandatesSuperpowers(t *testing.T) {
	out, err := Render(t.TempDir(), "developer", Data{Name: "developer-jason", Role: "developer", Goal: "build /health", JobID: 3, StorageRetentionDays: 60})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"developer-jason", "build /health",
		"executing-plans", "test-driven-development", "verification-before-completion",
		"omo inbox", "omo done", "omo wait", "omo step", "omo agent list", "Never invoke subagents",
		"plain command beginning with `omo`", "act as a transparent", "then resume what you were",
		"60 active office days", "last modification",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("developer prompt missing %q", want)
		}
	}
}

func TestRenderAllRoles(t *testing.T) {
	for _, role := range []string{"ceo", "product_manager", "developer", "reviewer", "freelancer", "smokealarm", "firefighter"} {
		out, err := Render(t.TempDir(), role, Data{Name: "x", Role: role, Goal: "g"})
		if err != nil {
			t.Errorf("render %s: %v", role, err)
			continue
		}
		for _, want := range []string{"internal state is strictly off-limits", ".omo/omo.db", ".omo/omo.yaml", "SQLite database directly", "--goal-file"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s prompt missing common safeguard %q", role, want)
			}
		}
	}
}

func TestCoordinationPromptsTeachPipeliningAndSafetyControls(t *testing.T) {
	tests := map[string][]string{
		"ceo":             {"--developer-models", "--force-developer-model", "omo office halt-spawns", "omo type", "provisional", "--goal-file", "Default to handing off", "concise report", "follow-up questions"},
		"product_manager": {"--model <profile>", "rolling batch", "provisional contract", "integration/alignment", "keeps excess jobs queued", "--goal-file"},
		"developer":       {"dependent API", "provisional", "alignment job"},
		"reviewer":        {"truly small", "commit", "Reject substantive", "provisional cross-service contract"},
		"smokealarm":      {"prior smoke runs", "at most ONE incident", "NEVER", "`omo wait`", "ALWAYS end", "`omo done", "n is 0 or 1"},
		"freelancer":      {"dedicated worktree", "Do NOT exit", "follow-up questions", "return to `omo wait`"},
		"firefighter":     {"omo estop", "immediately terminate", "omo type", "minimum safe input", "restart only"},
	}
	for role, wants := range tests {
		out, err := Render(t.TempDir(), role, Data{Name: role + "-x", Role: role, Goal: "g", JobID: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s prompt missing %q", role, want)
			}
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

func TestRenderExposesPathReferencesToTemplates(t *testing.T) {
	out, err := Render(t.TempDir(), "developer", Data{
		Name: "developer-paths", Role: "developer", Goal: "g",
		Paths: []PathReference{
			{Label: "workspace", Path: "/office/.omo/worktrees/api-4", Description: "current worktree"},
			{Label: "repo:api", Path: "/repos/api", Description: "configured checkout"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REFERENCE PATHS", "workspace", "/office/.omo/worktrees/api-4", "repo:api", "/repos/api"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered prompt missing %q:\n%s", want, out)
		}
	}
}
