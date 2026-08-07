package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
