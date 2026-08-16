package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

func TestRootHelpMentionsOffice(t *testing.T) {
	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "one-man-office") {
		t.Fatalf("help output missing product name:\n%s", out.String())
	}
}

func TestResolveSetupProviderDetectsAndAllowsOverride(t *testing.T) {
	detected := func() (agentcli.Provider, bool) { return agentcli.Claude, true }

	provider, automatic, err := resolveSetupProvider("auto", detected)
	if err != nil || provider != agentcli.Claude || !automatic {
		t.Fatalf("automatic selection = (%q, %v, %v)", provider, automatic, err)
	}

	provider, automatic, err = resolveSetupProvider("gemini", detected)
	if err != nil || provider != agentcli.Gemini || automatic {
		t.Fatalf("explicit override = (%q, %v, %v)", provider, automatic, err)
	}

	provider, automatic, err = resolveSetupProvider("auto", func() (agentcli.Provider, bool) { return "", false })
	if err != nil || provider != agentcli.Claude || automatic {
		t.Fatalf("no-CLI fallback = (%q, %v, %v)", provider, automatic, err)
	}

	if _, _, err := resolveSetupProvider("unknown", detected); err == nil {
		t.Fatal("invalid explicit provider accepted")
	}
}

func TestSetupWarnsWhenGeminiIsExplicitlySelected(t *testing.T) {
	dir := t.TempDir()
	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"setup", "--agent-cli", "gemini", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "WARNING: Gemini is not recommended") {
		t.Fatalf("setup output missing Gemini recommendation warning:\n%s", got)
	}
}

func TestSetupUpdateReplacesTemplatesFromCLI(t *testing.T) {
	dir := t.TempDir()
	if _, err := office.Setup(dir); err != nil {
		t.Fatal(err)
	}
	common := filepath.Join(dir, prompts.Dir, "common.md")
	if err := os.WriteFile(common, []byte("CUSTOM PROMPT"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"setup", "--update", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("setup --update: %v", err)
	}
	if !strings.Contains(out.String(), "replaced .omo/messages/") || !strings.Contains(out.String(), "replaced .omo/prompts/") {
		t.Fatalf("update output does not describe both replacements:\n%s", out.String())
	}
	if raw, err := os.ReadFile(common); err != nil || strings.Contains(string(raw), "CUSTOM PROMPT") {
		t.Fatalf("common prompt was not replaced: %q, err %v", raw, err)
	}
}

func TestRepoCommandsAddListAndRemove(t *testing.T) {
	officeDir := t.TempDir()
	if _, err := office.Setup(officeDir); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(t.TempDir(), "payments")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(officeDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	run := func(args ...string) string {
		t.Helper()
		cmd := Root("test")
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("omo %s: %v", strings.Join(args, " "), err)
		}
		return out.String()
	}
	if out := run("repo", "add", repoDir); !strings.Contains(out, "added repository payments") {
		t.Fatalf("unexpected add output: %s", out)
	}
	if out := run("repo", "list"); !strings.Contains(out, "payments\t"+repoDir) {
		t.Fatalf("unexpected list output: %s", out)
	}
	if out := run("repo", "remove", "payments"); !strings.Contains(out, "removed repository payments") {
		t.Fatalf("unexpected remove output: %s", out)
	}
	cfg, err := config.Load(filepath.Join(officeDir, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.Repos["payments"]; exists {
		t.Fatal("removed repository is still configured")
	}
}
