package company

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func projectHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OMO_HOME", filepath.Join(dir, "global"))
	return dir
}

func TestProjectsRequireExplicitTrustAndCanonicalizeAliases(t *testing.T) {
	dir := projectHome(t)
	project, err := CreateProject(context.Background(), filepath.Join(dir, "office"), "")
	if err != nil {
		t.Fatal(err)
	}
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

func TestProjectCreationDoesNotOverwriteOrAcceptRelativeDestinations(t *testing.T) {
	dir := projectHome(t)
	for _, target := range []string{"relative", "../escape", dir, filepath.Join(dir, "missing", "nested")} {
		if _, err := CreateProject(context.Background(), target, ""); err == nil {
			t.Fatalf("accepted %q", target)
		}
	}
	if _, err := TrustProject(dir); err == nil {
		t.Fatal("trusted directory without office config")
	}
	if _, err := CreateProject(context.Background(), filepath.Join(dir, "bad"), "ext::sh -c touch bad"); err == nil {
		t.Fatal("accepted executable git transport")
	}
	if _, err := os.Stat(filepath.Join(dir, "bad")); !os.IsNotExist(err) {
		t.Fatalf("bad source created destination: %v", err)
	}
}

func TestCloneProjectUsesLiteralPathsAndScaffoldsOffice(t *testing.T) {
	dir := projectHome(t)
	source := filepath.Join(dir, "source")
	cmd := exec.Command("git", "init", "-q", source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git: %s %v", out, err)
	}
	destination := filepath.Join(dir, "office with spaces; dollar$")
	project, err := CreateProject(context.Background(), destination, source)
	if err != nil {
		t.Fatal(err)
	}
	if project.Path != destination || !project.Available {
		t.Fatalf("project: %+v", project)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".omo", "omo.yaml")); err != nil {
		t.Fatal(err)
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
