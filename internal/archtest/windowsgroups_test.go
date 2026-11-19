package archtest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// windowsGroup is one entry of the Windows job's matrix.
type windowsGroup struct {
	Name   string `yaml:"name"`
	Only   string `yaml:"only"`
	Except string `yaml:"except"`
	Shard  int    `yaml:"shard"`
	Shards int    `yaml:"shards"`
}

// The Windows job tests the Go packages in groups: some name their packages
// (only), one takes all the rest (except). A package named by a group but
// missing from the exclusions would be tested twice; one excluded but named
// by no group would not be tested at all. A group may be split into parts
// (shard k of shards n), each of which runs every n-th test: a missing part
// would leave those tests out, a repeated one would run them twice.
func TestWindowsTestGroupsCoverEveryPackageOnce(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "pull-request.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Include []windowsGroup `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	groups := workflow.Jobs["windows-tests"].Strategy.Matrix.Include
	if len(groups) == 0 {
		t.Fatal("the job windows-tests has no matrix entries")
	}
	if err := checkWindowsGroups(groups); err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		for _, name := range packagesOf(g.Only) {
			if fi, err := os.Stat(filepath.Join("..", name)); err != nil || !fi.IsDir() {
				t.Errorf("the Windows job names the package internal/%s, which does not exist", name)
			}
		}
	}
}

var windowsGroupPattern = regexp.MustCompile(`^internal/\(([a-z0-9|]+)\)$`)

// packagesOf returns the package names of an only or except value, sorted,
// or nil when the value does not have the form internal/(a|b).
func packagesOf(value string) []string {
	m := windowsGroupPattern.FindStringSubmatch(value)
	if m == nil {
		return nil
	}
	names := strings.Split(m[1], "|")
	slices.Sort(names)
	return names
}

// checkWindowsGroups reports the first way in which the groups do not test
// every package and every test exactly once.
func checkWindowsGroups(groups []windowsGroup) error {
	parts := map[string][]windowsGroup{} // by the only value
	var named, except []string
	rest := 0
	for _, g := range groups {
		switch {
		case g.Only != "" && g.Except != "":
			return fmt.Errorf("the group %q has both only and except", g.Name)
		case g.Except != "":
			rest++
			if except = packagesOf(g.Except); except == nil {
				return fmt.Errorf("the group %q has an except that is not of the form internal/(a|b): %q", g.Name, g.Except)
			}
			if g.Shard != 0 || g.Shards != 0 {
				return fmt.Errorf("the group %q takes the rest of the packages and cannot be split into parts", g.Name)
			}
		default:
			if packagesOf(g.Only) == nil {
				return fmt.Errorf("the group %q has an only that is not of the form internal/(a|b): %q", g.Name, g.Only)
			}
			if _, seen := parts[g.Only]; !seen {
				named = append(named, packagesOf(g.Only)...)
			}
			parts[g.Only] = append(parts[g.Only], g)
		}
	}
	if rest != 1 {
		return fmt.Errorf("found %d groups with except, want exactly one", rest)
	}
	slices.Sort(named)
	if len(slices.Compact(slices.Clone(named))) != len(named) {
		return fmt.Errorf("a package is named by two groups: %v", named)
	}
	if !slices.Equal(named, except) {
		return fmt.Errorf("the groups name %v, the last group excludes %v: they must be the same", named, except)
	}
	for only, list := range parts {
		if len(list) == 1 && list[0].Shard == 0 && list[0].Shards == 0 {
			continue // one group, not split
		}
		var have []int
		for _, g := range list {
			if g.Shards != len(list) {
				return fmt.Errorf("the group %q says it is split into %d parts, but %d groups test %s", g.Name, g.Shards, len(list), only)
			}
			have = append(have, g.Shard)
		}
		slices.Sort(have)
		for i, shard := range have {
			if shard != i+1 {
				return fmt.Errorf("the parts of %s are %v, want each of 1 to %d once", only, have, len(list))
			}
		}
	}
	return nil
}

// The check itself refuses every way of losing or repeating tests.
func TestWindowsTestGroupsCheckRefusesGaps(t *testing.T) {
	good := []windowsGroup{
		{Name: "a 1/2", Only: "internal/(a)", Shard: 1, Shards: 2},
		{Name: "a 2/2", Only: "internal/(a)", Shard: 2, Shards: 2},
		{Name: "b", Only: "internal/(b|c)"},
		{Name: "rest", Except: "internal/(a|b|c)"},
	}
	if err := checkWindowsGroups(good); err != nil {
		t.Fatalf("a complete set of groups is refused: %v", err)
	}
	change := func(edit func([]windowsGroup) []windowsGroup) []windowsGroup {
		return edit(slices.Clone(good))
	}
	bad := map[string][]windowsGroup{
		"a part is missing":           change(func(g []windowsGroup) []windowsGroup { return g[1:] }),
		"a part is there twice":       change(func(g []windowsGroup) []windowsGroup { g[1].Shard = 1; return g }),
		"the part count is wrong":     change(func(g []windowsGroup) []windowsGroup { g[0].Shards, g[1].Shards = 3, 3; return g }),
		"a package is named twice":    change(func(g []windowsGroup) []windowsGroup { g[2].Only = "internal/(a|b|c)"; return g }),
		"a package is not excluded":   change(func(g []windowsGroup) []windowsGroup { g[3].Except = "internal/(a|b)"; return g }),
		"a package is only excluded":  change(func(g []windowsGroup) []windowsGroup { g[2].Only = "internal/(b)"; return g }),
		"no group takes the rest":     change(func(g []windowsGroup) []windowsGroup { return g[:3] }),
		"two groups take the rest":    change(func(g []windowsGroup) []windowsGroup { return append(g, g[3]) }),
		"the rest is split":           change(func(g []windowsGroup) []windowsGroup { g[3].Shard, g[3].Shards = 1, 1; return g }),
		"an only has another form":    change(func(g []windowsGroup) []windowsGroup { g[2].Only = "internal/b"; return g }),
		"a group has only and except": change(func(g []windowsGroup) []windowsGroup { g[2].Except = "internal/(a)"; return g }),
		"an unsplit group has a twin": change(func(g []windowsGroup) []windowsGroup { return append(g, g[2]) }),
	}
	for name, groups := range bad {
		if err := checkWindowsGroups(groups); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
