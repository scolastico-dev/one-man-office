package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
)

func TestOfficeTrustApprovalAndNoninteractivePolicy(t *testing.T) {
	for _, tt := range []struct {
		name, input                 string
		terminal, explicit, allowed bool
	}{
		{name: "accept", input: "yes\n", terminal: true, allowed: true},
		{name: "decline", input: "no\n", terminal: true},
		{name: "eof", terminal: true},
		{name: "pipe cannot approve", input: "yes\n"},
		{name: "explicit automation", explicit: true, allowed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OMO_HOME", t.TempDir())
			old := inputIsTerminal
			t.Cleanup(func() { inputIsTerminal = old })
			inputIsTerminal = func(io.Reader) bool { return tt.terminal }
			cmd := Root("test")
			cmd.SetIn(strings.NewReader(tt.input))
			var out bytes.Buffer
			cmd.SetOut(&out)
			dir := t.TempDir()
			canonical, err := ensureOfficeTrust(cmd, dir, tt.explicit)
			if (err == nil) != tt.allowed {
				t.Fatalf("allowed=%v err=%v", tt.allowed, err)
			}
			h, loadErr := globalhome.Open()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			want, _ := globalhome.CanonicalOffice(dir)
			if h.IsTrusted(want) != tt.allowed {
				t.Fatalf("persisted trust=%v", h.Config.TrustedOffices)
			}
			if tt.allowed {
				if canonical != want {
					t.Fatalf("canonical=%s", canonical)
				}
				inputIsTerminal = func(io.Reader) bool { return false }
				cmd.SetIn(strings.NewReader(""))
				if _, err := ensureOfficeTrust(cmd, dir, false); err != nil {
					t.Fatalf("trusted office prompted again: %v", err)
				}
			}
		})
	}
}

func TestRootTrustPrecedesConfigAndCannotBeSkipped(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir(filepath.Join(dir, ".omo"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".omo", "omo.yaml")
	if err := os.WriteFile(path, []byte("invalid: ["), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := Root("test")
	cmd.SetIn(strings.NewReader("yes\n"))
	cmd.SetArgs([]string{"--mock", "--no-tui", "--skip-startup-checks"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("trust bypassed: %v", err)
	}
	cmd = Root("test")
	cmd.SetArgs([]string{"--trust-office", "--mock", "--no-tui", "--skip-startup-checks"})
	if err := cmd.Execute(); err == nil || strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("explicit trust did not reach config validation: %v", err)
	}
}

func TestOfficeTrustDoesNotPromptOnNullDevice(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	cmd := Root("test")
	cmd.SetIn(input)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if _, err := ensureOfficeTrust(cmd, t.TempDir(), false); err == nil {
		t.Fatal("null input trusted office")
	}
	if out.Len() != 0 {
		t.Fatalf("prompted noninteractive device: %q", out.String())
	}
}
