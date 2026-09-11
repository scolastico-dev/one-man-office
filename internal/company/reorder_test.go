package company

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/office"
)

func TestReorderProjectsReturnsRequestedOrderAndPersistsUnavailableProjects(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", filepath.Join(root, "global"))
	officePaths := []string{filepath.Join(root, "first"), filepath.Join(root, "stale"), filepath.Join(root, "last")}
	for _, path := range officePaths {
		if err := os.MkdirAll(filepath.Join(path, ".omo"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, office.ConfigPath), []byte("repos: []\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	home, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range officePaths {
		if err := home.Trust(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(officePaths[1]); err != nil {
		t.Fatal(err)
	}
	want := []string{officePaths[2], officePaths[1], officePaths[0]}
	projects, err := ReorderProjects(want)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(projects))
	for _, project := range projects {
		got = append(got, project.Path)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("returned projects = %v, want %v", got, want)
	}
	if projects[1].Available {
		t.Fatalf("stale project marked available: %+v", projects[1])
	}
	reopened, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reopened.Config.TrustedOffices, want) {
		t.Fatalf("reopened trusted offices = %v, want %v", reopened.Config.TrustedOffices, want)
	}
}
