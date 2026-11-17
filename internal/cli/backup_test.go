package cli

import (
	"archive/zip"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// TestConfigExportAndImport writes the settings with omo config export,
// changes one, and imports the file again: without --yes nothing changes,
// with it the old value is back.
func TestConfigExportAndImport(t *testing.T) {
	e := startAI(t)
	file := filepath.Join(t.TempDir(), "omo.zip")
	out := mustExecute(t, "config", "export", file, "--with-db")
	contains(t, out, "keep it private")
	zr, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	zr.Close()
	if !names["manifest.json"] || !names["settings.yaml"] || !names["omo.db"] || !names["projects/api.yaml"] {
		t.Fatalf("entries %v", names)
	}

	ctx := context.Background()
	if err := e.user.Do(ctx, http.MethodPatch, "/api/settings", map[string]any{"limits": map[string]any{"tasks": 9}}, nil); err != nil {
		t.Fatal(err)
	}
	tasks := func() int {
		var s api.SettingsResponse
		if err := e.user.Do(ctx, http.MethodGet, "/api/settings", nil, &s); err != nil {
			t.Fatal(err)
		}
		return s.Settings.Limits.Tasks
	}
	out = mustExecute(t, "config", "import", file)
	contains(t, out, "replace  settings.yaml")
	contains(t, out, "Run again with --yes")
	if tasks() != 9 {
		t.Fatal("a check changed the settings")
	}
	out = mustExecute(t, "config", "import", file, "--yes")
	contains(t, out, "The replaced files are kept in")
	if tasks() == 9 {
		t.Fatal("the import did not change the settings")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("OMO_HOME"), "omo.db.import")); !os.IsNotExist(err) {
		t.Fatal("the database was imported without --with-db")
	}

	bad := filepath.Join(t.TempDir(), "bad.zip")
	os.WriteFile(bad, []byte("no zip"), 0o600)
	if _, err := execute(ctx, "config", "import", bad); err == nil || !strings.Contains(err.Error(), "not a zip") {
		t.Fatalf("err = %v", err)
	}
}
