package settings

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Every settings producer uses this writer. Its output must respect the same
// byte limit as that section's reader, regardless of serialization expansion.
func TestSettingsWriterRespectsEverySectionReadLimit(t *testing.T) {
	s, _, _ := openTest(t)
	for _, k := range s.kinds {
		t.Run(k.section, func(t *testing.T) {
			path := k.file
			if path == "" {
				path = filepath.Join(k.dir, "size-check"+k.ext)
			}
			atLimit := bytes.Repeat([]byte(" "), int(k.limit))
			if err := s.writeFile(path, atLimit); err != nil {
				t.Fatalf("exact read limit refused: %v", err)
			}
			state := *s.files[path]
			if err := s.writeFile(path, append(atLimit, ' ')); err == nil {
				t.Fatal("write larger than reader limit accepted")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, atLimit) || *s.files[path] != state {
				t.Fatal("oversized write changed file or recorded state")
			}
		})
	}
}
