package office

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Layout describes how an office relates to the repositories it works on.
// omo supports both shapes without configuration: run it inside one repo, or
// in the parent directory of a microservice landscape.
type Layout string

const (
	// LayoutSingleRepo: the office directory is itself a git repository.
	LayoutSingleRepo Layout = "single-repo"
	// LayoutLandscape: the office directory holds several repositories.
	LayoutLandscape Layout = "landscape"
	// LayoutUnknown: no repository found — repos must be configured by hand.
	LayoutUnknown Layout = "unknown"
)

// IsRepo reports whether dir is a git checkout. .git is a directory in a
// normal clone and a file in a worktree or submodule.
func IsRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// DiscoverRepos finds the repositories an office should offer, keyed by
// directory name. The office's own repo wins: a monorepo that vendors other
// checkouts is one repo, not many.
func DiscoverRepos(officeDir string) (map[string]string, Layout) {
	abs, err := filepath.Abs(officeDir)
	if err != nil {
		abs = officeDir
	}
	if IsRepo(abs) {
		return map[string]string{filepath.Base(abs): abs}, LayoutSingleRepo
	}
	repos := map[string]string{}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return repos, LayoutUnknown
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue // .omo, .git, dotfiles: never candidates
		}
		p := filepath.Join(abs, e.Name())
		if IsRepo(p) {
			repos[e.Name()] = p
		}
	}
	if len(repos) == 0 {
		return repos, LayoutUnknown
	}
	return repos, LayoutLandscape
}

// SortedKeys returns repo keys in a stable order for display.
func SortedKeys(repos map[string]string) []string {
	out := make([]string, 0, len(repos))
	for k := range repos {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
