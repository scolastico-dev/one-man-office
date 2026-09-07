package controlplane

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
)

func TestDarwinRegistrationRejectsRelativeClaudeAccountNamespace(t *testing.T) {
	for _, variable := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR"} {
		t.Run(variable, func(t *testing.T) {
			profile := config.Profile{Cmd: "claude", Env: map[string]string{variable: "relative-account"}}
			err := normalizeCredentialRoots(profile, t.TempDir(), "darwin")
			if err == nil || !strings.Contains(err.Error(), variable) || !strings.Contains(err.Error(), "absolute") {
				t.Fatalf("relative account namespace was not rejected with guidance: %v", err)
			}
			if profile.Env[variable] != "relative-account" {
				t.Fatal("rejected account namespace was silently rewritten")
			}
		})
	}
}

func TestDarwinRegistrationPreservesAbsoluteClaudeAccountNamespace(t *testing.T) {
	// Keychain selection hashes the original spelling, not a cleaned path.
	root := t.TempDir() + "/../account"
	profile := config.Profile{Provider: "claude", Cmd: "wrapper", Env: map[string]string{"CLAUDE_CONFIG_DIR": root, "CLAUDE_SECURESTORAGE_CONFIG_DIR": ""}}
	if err := normalizeCredentialRoots(profile, t.TempDir(), "darwin"); err != nil {
		t.Fatal(err)
	}
	if profile.Env["CLAUDE_CONFIG_DIR"] != root || profile.Env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != "" {
		t.Fatalf("account namespace changed: %#v", profile.Env)
	}
}

func TestRegistrationNormalizesRelativeFileCredentialsOutsideDarwin(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			office := t.TempDir()
			profile := config.Profile{Cmd: "claude", Env: map[string]string{"CLAUDE_SECURESTORAGE_CONFIG_DIR": "account"}}
			if err := normalizeCredentialRoots(profile, office, goos); err != nil {
				t.Fatal(err)
			}
			if got := profile.Env["CLAUDE_SECURESTORAGE_CONFIG_DIR"]; got != filepath.Join(office, "account") {
				t.Fatalf("file credentials resolved outside office: %q", got)
			}
		})
	}
}

func TestRegistrationPreservesLiteralClaudeCredentialDirectory(t *testing.T) {
	office := t.TempDir()
	profile := config.Profile{Cmd: "claude", Env: map[string]string{"CLAUDE_SECURESTORAGE_CONFIG_DIR": " account "}}
	if err := normalizeCredentialRoots(profile, office, "linux"); err != nil {
		t.Fatal(err)
	}
	if got := profile.Env["CLAUDE_SECURESTORAGE_CONFIG_DIR"]; got != filepath.Join(office, " account ") {
		t.Fatalf("literal credential directory was changed: %q", got)
	}
}
