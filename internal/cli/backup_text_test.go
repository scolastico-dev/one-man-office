package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/homezip"
)

// The words that name each exported file kind in the descriptions of the
// export (the command's help and the Backup page).
var exportWords = map[string]string{
	"settings.yaml":     "general settings",
	"server.yaml":       "server",
	"projects/*.yaml":   "projects",
	"profiles/*.yaml":   "profiles",
	"roles/*.yaml":      "roles",
	"prompts/*.md":      "prompts",
	"usage/*.lua":       "usage scripts",
	"skillsets/*.yaml":  "skill sets",
	"plugins/*.yaml":    "plugin settings",
	"mcpservers/*.yaml": "MCP servers",
}

func TestExportDescriptionsNameEveryExportedKind(t *testing.T) {
	page, err := os.ReadFile("../../web/src/features/settings/backup/BackupPage.vue")
	if err != nil {
		t.Fatal(err)
	}
	var help string
	for _, c := range Root("test").Commands() {
		if c.Name() == "config" {
			for _, s := range c.Commands() {
				if s.Name() == "export" {
					help = s.Long
				}
			}
		}
	}
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for _, e := range homezip.Exported {
		word, ok := exportWords[e.Pattern]
		if !ok {
			t.Fatalf("%s is exported but no description names it; add it to the export help and the Backup page", e.Pattern)
		}
		if !strings.Contains(flat(help), word) {
			t.Errorf("omo config export --help does not name %q (%s)", word, e.Pattern)
		}
		if !strings.Contains(flat(string(page)), word) {
			t.Errorf("the Backup page does not name %q (%s)", word, e.Pattern)
		}
	}
}
