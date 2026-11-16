// Package buildinfo knows where the source of a build lives: the one place
// that holds the repository's address and reads the version string the build
// stamps (-X main.version=...).
package buildinfo

import (
	"regexp"
	"strings"
)

// Repository is the web address of omo's source repository.
const Repository = "https://github.com/scolastico-dev/one-man-office"

// Source is the page of a version on the repository's site. A version
// that names no known release or commit has the zero Source.
type Source struct {
	// URL is the page of the commit or release; "" for an unknown form.
	URL string
	// Commit is the commit hash as the version spells it (abbreviated or
	// full); "" for a release or an unknown form.
	Commit string
}

const (
	semver = `v?[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z][0-9A-Za-z.]*)?`
	hash   = `[0-9a-f]{7,40}`
)

var (
	// 0.11.0-476-g8a63736 and ...-dirty: git describe after the last tag.
	described = regexp.MustCompile(`^` + semver + `-[0-9]+-g(` + hash + `)(?:-dirty)?$`)
	// 8a63736 and ...-dirty: git describe --always in a clone without tags.
	bareHash = regexp.MustCompile(`^(` + hash + `)(?:-dirty)?$`)
	// nightly-<commit>: the nightly build.
	nightly = regexp.MustCompile(`^nightly-(` + hash + `)$`)
	// 0.12.0 and v0.12.0: a release build on its tag.
	release = regexp.MustCompile(`^` + semver + `$`)
)

// SourceOf returns the page of version on the repository's site: a build
// of a commit links to the commit, a release to its release page (the tag
// is the version itself, with or without its "v"), anything else, such as
// "dev" or a dirty tree on a tag, to nothing.
func SourceOf(version string) Source {
	for _, re := range []*regexp.Regexp{described, bareHash, nightly} {
		if m := re.FindStringSubmatch(version); m != nil {
			return Source{URL: Repository + "/commit/" + m[1], Commit: m[1]}
		}
	}
	if release.MatchString(version) && !strings.HasSuffix(version, "-dirty") {
		return Source{URL: Repository + "/releases/tag/" + version}
	}
	return Source{}
}
