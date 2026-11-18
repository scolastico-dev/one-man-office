package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The Windows job tests the Go packages in groups: some name their packages
// (only), one takes all the rest (except). A package named by a group but
// missing from the exclusions would be tested twice; one excluded but named
// by no group would not be tested at all. The two lists must be the same.
func TestWindowsTestGroupsCoverEveryPackageOnce(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "pull-request.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	names := func(key string) (lines int, out []string) {
		for _, m := range regexp.MustCompile(`(?m)^\s+(?:- )?`+key+`: internal/\(([a-z0-9|]+)\)\s*$`).FindAllStringSubmatch(text, -1) {
			lines++
			out = append(out, strings.Split(m[1], "|")...)
		}
		slices.Sort(out)
		return lines, out
	}
	onlyLines, only := names("only")
	exceptLines, except := names("except")
	if onlyLines == 0 || exceptLines != 1 {
		t.Fatalf("found %d groups with only and %d with except; want at least one and exactly one", onlyLines, exceptLines)
	}
	if len(slices.Compact(slices.Clone(only))) != len(only) {
		t.Fatalf("a package is named by two groups: %v", only)
	}
	if !slices.Equal(only, except) {
		t.Fatalf("the groups name %v, the last group excludes %v: they must be the same", only, except)
	}
	for _, name := range only {
		if fi, err := os.Stat(filepath.Join("..", name)); err != nil || !fi.IsDir() {
			t.Errorf("the Windows job names the package internal/%s, which does not exist", name)
		}
	}
}
