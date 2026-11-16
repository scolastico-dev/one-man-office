package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The export outcome of a project is kept twice: in the pins, which the
// engine settles from, and on the ticket's project, which answers and events
// show. Only keepExportFailure and keepLeftOut write both; a direct pin
// write elsewhere would leave the web showing a stale state.
func TestExportOutcomeIsOnlyWrittenThroughTheKeepHelpers(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "pins.go" || f == "export.go" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range []string{".setExportFailed(", ".setLeftOut("} {
			if strings.Contains(string(raw), call) {
				t.Errorf("%s calls %s: use keepExportFailure or keepLeftOut", f, call)
			}
		}
	}
}
