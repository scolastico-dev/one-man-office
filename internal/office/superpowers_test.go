package office

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
)

func TestSuperpowersWarningsChecksDistinctProfileEnvironments(t *testing.T) {
	cli := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nprintf '{\"installed\":[],\"available\":[]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedRoot := t.TempDir()
	manifest := filepath.Join(installedRoot, "plugins", "cache", "remote", "superpowers", "6.3.0", ".codex-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"superpowers"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Models = map[string]config.Profile{
		"installed": {Cmd: cli, Provider: agentcli.Codex, Env: map[string]string{"CODEX_HOME": installedRoot}},
		"missing":   {Cmd: cli, Provider: agentcli.Codex, Env: map[string]string{"CODEX_HOME": t.TempDir()}},
	}
	warnings := SuperpowersWarnings(context.Background(), &cfg)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want exactly one for the missing environment", warnings)
	}
}
