package company

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/office"
)

func projectHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OMO_HOME", filepath.Join(dir, "global"))
	return dir
}

func testOffice(t *testing.T, path string) Project {
	t.Helper()
	if _, err := office.SetupWithAgentCLI(path, agentcli.Claude); err != nil {
		t.Fatal(err)
	}
	project, err := TrustProject(path)
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func TestProjectsRequireExplicitTrustAndCanonicalizeAliases(t *testing.T) {
	dir := projectHome(t)
	project := testOffice(t, filepath.Join(dir, "office"))
	if got, err := TrustedProject(project.Path); err != nil || got != project.Path {
		t.Fatalf("trusted path %q: %v", got, err)
	}
	untrusted := filepath.Join(dir, "untrusted")
	if err := os.Mkdir(untrusted, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := TrustedProject(untrusted); err == nil {
		t.Fatal("untrusted path accepted")
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(project.Path, alias); err == nil {
		if got, err := TrustedProject(alias); err != nil || got != project.Path {
			t.Fatalf("alias: %q %v", got, err)
		}
	}
	projects, err := Projects()
	if err != nil || len(projects) != 1 || !projects[0].Available {
		t.Fatalf("projects: %+v %v", projects, err)
	}
}

func TestUntrustProjectRemovesUnavailableAndUnknownOffices(t *testing.T) {
	dir := projectHome(t)
	project := testOffice(t, filepath.Join(dir, "office"))
	if err := os.RemoveAll(project.Path); err != nil {
		t.Fatal(err)
	}
	if err := UntrustProject(project.Path); err != nil {
		t.Fatal(err)
	}
	projects, err := Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("stale project remained: %+v", projects)
	}
	if err := UntrustProject(filepath.Join(dir, "unknown")); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRequestValidationRejectsUnsafeDestinationsAndSources(t *testing.T) {
	dir := projectHome(t)
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"relative", "../escape", file, filepath.Join(dir, "missing", "nested")} {
		if _, _, _, err := ValidateProjectRequest(target, "", false); err == nil {
			t.Fatalf("accepted %q", target)
		}
	}
	if _, err := TrustProject(dir); err == nil {
		t.Fatal("trusted directory without office config")
	}
	if _, _, _, err := ValidateProjectRequest(filepath.Join(dir, "bad"), "ext::sh -c touch bad", true); err == nil {
		t.Fatal("accepted executable git transport")
	}
	if _, err := os.Stat(filepath.Join(dir, "bad")); !os.IsNotExist(err) {
		t.Fatalf("bad source created destination: %v", err)
	}
}

func TestProjectRequestValidationAllowsCreateDirectoriesAndEmptyCloneTargets(t *testing.T) {
	dir := projectHome(t)
	createTarget := filepath.Join(dir, "existing-create")
	if err := os.Mkdir(createTarget, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(createTarget, "notes.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ValidateProjectRequest(createTarget, "", false); err != nil {
		t.Fatalf("create rejected non-empty directory: %v", err)
	}
	cloneTarget := filepath.Join(dir, "existing-clone")
	if err := os.Mkdir(cloneTarget, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ValidateProjectRequest(cloneTarget, source, true); err != nil {
		t.Fatalf("clone rejected empty directory: %v", err)
	}
}

func TestProjectRequestValidationRejectsConfiguredAndNonEmptyCloneTargets(t *testing.T) {
	dir := projectHome(t)
	configured := filepath.Join(dir, "configured")
	if err := os.MkdirAll(filepath.Join(configured, ".omo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configured, ".omo", "omo.yaml"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ValidateProjectRequest(configured, "", false); err == nil || !strings.Contains(err.Error(), "use Load and trust") {
		t.Fatalf("configured destination error = %v", err)
	}
	nonEmpty := filepath.Join(dir, "non-empty")
	if err := os.Mkdir(nonEmpty, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ValidateProjectRequest(nonEmpty, source, true); err == nil || !strings.Contains(err.Error(), "empty directory") {
		t.Fatalf("non-empty clone destination error = %v", err)
	}
}

func TestCloneProjectRequestPreservesLiteralPaths(t *testing.T) {
	dir := projectHome(t)
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "office with spaces; dollar$")
	canonical, parent, validatedSource, err := ValidateProjectRequest(destination, source, true)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != destination || parent != dir || validatedSource != source {
		t.Fatalf("request paths: %q %q %q", canonical, parent, validatedSource)
	}
}

func TestCloneSourceValidationAllowsSupportedSourcesAndRejectsExecutableTransports(t *testing.T) {
	dir := projectHome(t)
	local := filepath.Join(dir, "local-source")
	if err := os.Mkdir(local, 0700); err != nil {
		t.Fatal(err)
	}
	for n, source := range []string{"https://example.com/repo.git", "ssh://git@example.com/repo.git", local} {
		t.Run("safe", func(t *testing.T) {
			if _, _, _, err := ValidateProjectRequest(filepath.Join(dir, fmt.Sprintf("safe-%d", n)), source, true); err != nil {
				t.Fatalf("source rejected: %v", err)
			}
		})
	}
	for n, source := range []string{"http://example.com/repo.git", "file:///tmp/repo", "ext::sh -c touch pwned", "--upload-pack=sh", "ssh://-bad.example/repo.git", "https://example.com/repo\n.git"} {
		t.Run("unsafe", func(t *testing.T) {
			if _, _, _, err := ValidateProjectRequest(filepath.Join(dir, fmt.Sprintf("unsafe-%d", n)), source, true); err == nil {
				t.Fatal("source accepted")
			}
		})
	}
}

func TestShellDirectoryDefaultsToHomeWithoutOffice(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := shellDirectory("")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("shell home = %q, want %q", got, want)
	}
}
