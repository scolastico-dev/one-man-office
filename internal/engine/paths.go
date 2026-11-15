package engine

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// maxSlug is the longest title part of a branch name.
const maxSlug = 40

// BranchName derives the branch of ticket n from its number and title:
// omo/<n>-<slug>, where the slug is the title in lower case with every run
// of characters outside a-z and 0-9 replaced by one dash, without dashes at
// either end, and cut to at most 40 characters at the last dash within the
// limit (or exactly at the limit when there is none). A title without any
// such character gives omo/<n>.
//
// exists reports whether a branch is taken; the caller answers false for the
// branch already recorded on the ticket. A taken name gets -2, -3, ...
// appended until one is free, at most up to -maxBranchSuffix; past that, or
// when exists fails, BranchName returns an error instead of a name. A nil
// exists takes nothing.
//
// The result is always a valid git branch name: one path component below
// omo/ made of a-z, 0-9 and inner dashes. It is unique per ticket, because
// the part up to the first dash after omo/ is the ticket number.
func BranchName(n int64, title string, exists func(string) (bool, error)) (string, error) {
	base := "omo/" + strconv.FormatInt(n, 10)
	if slug := slugify(title); slug != "" {
		base += "-" + slug
	}
	if exists == nil {
		return base, nil
	}
	for i := 1; i <= maxBranchSuffix; i++ {
		name := base
		if i > 1 {
			name += "-" + strconv.Itoa(i)
		}
		taken, err := exists(name)
		if err != nil {
			return "", fmt.Errorf("cannot check whether branch %s exists: %w", name, err)
		}
		if !taken {
			return name, nil
		}
	}
	return "", fmt.Errorf("the branches %s to %s-%d all exist", base, base, maxBranchSuffix)
}

// maxBranchSuffix is the highest number BranchName appends to a taken name.
const maxBranchSuffix = 100

// slugify implements the slug rule of BranchName.
func slugify(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	slug := b.String() // no dash at either end
	if len(slug) <= maxSlug {
		return slug
	}
	cut := slug[:maxSlug]
	if slug[maxSlug] != '-' {
		if i := strings.LastIndexByte(cut, '-'); i > 0 {
			cut = cut[:i]
		}
	}
	return strings.Trim(cut, "-")
}

// WorktreeDir is the folder of ticket n's worktree of project below root,
// the worktrees folder of omo's home.
func WorktreeDir(root, project string, n int64) string {
	return filepath.Join(root, project, strconv.FormatInt(n, 10))
}
