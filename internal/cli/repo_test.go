package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/office"
)

func TestRepoAddKeepsGitIntegratedConfigPortable(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	officeDir := t.TempDir()
	repository := t.TempDir()
	runGitCommand(t, officeDir, "init")
	runGitCommand(t, repository, "init")
	if _, err := office.Setup(officeDir); err != nil {
		t.Fatal(err)
	}
	if _, err := office.EnableGitIntegration(officeDir); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(officeDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	cmd := Root("test")
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"repo", "add", "external", repository})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(officeDir, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), filepath.ToSlash(repository)) {
		t.Fatalf("repo add persisted an absolute path:\n%s", raw)
	}
	want, err := filepath.Rel(officeDir, repository)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), filepath.ToSlash(want)) {
		t.Fatalf("repo add omitted relative path %q:\n%s", want, raw)
	}
}
