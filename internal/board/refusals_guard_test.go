package board

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Refusal texts say what the user or an agent sees and can do: columns by
// their titles on the board, never by identifier, and no API route.
func TestRefusalTextsNameNeitherColumnIdsNorRoutes(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	ids := regexp.MustCompile(`in_progress`)
	routes := regexp.MustCompile(`"[^"\n]*(\buse /[a-z]|\bin base_branches)`)
	var bad []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		for i := 0; ; {
			at := strings.Index(src[i:], "api.Errorf(")
			if at < 0 {
				break
			}
			start := i + at
			depth, end := 0, start
			for end = start; end < len(src); end++ {
				if src[end] == '(' {
					depth++
				} else if src[end] == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			call := src[start : end+1]
			i = end
			// The hint of an unknown column lists the ids a request takes.
			if strings.Contains(call, "unknown column") {
				continue
			}
			switch {
			case ids.MatchString(call), routes.MatchString(call):
				bad = append(bad, f+": "+strings.Join(strings.Fields(call), " "))
			case strings.Contains(call, ".Col") && !strings.Contains(call, "columnWords("):
				bad = append(bad, f+": column printed as identifier: "+strings.Join(strings.Fields(call), " "))
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("refusal texts with ids or routes:\n%s", strings.Join(bad, "\n"))
	}
}
