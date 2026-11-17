package skillsets

import (
	"path/filepath"
	"regexp"
	"strings"
)

// spaces are the runs a skill name normalises to one dash.
var spaces = regexp.MustCompile(`[\s_]+`)

// normalName is a skill name as names are compared: lower case, white
// space and underscores made dashes (as the skills CLI compares them).
func normalName(s string) string {
	return spaces.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
}

// FolderOf finds the folder of the skill skills.sh calls id (its skill id)
// and name in the checkout folder root, the way Find finds skills: every
// folder with a SKILL.md, not looking into a skill's own folder, maxDepth
// levels deep, folders starting with a dot skipped, links never followed.
// The best match wins: a folder named id whose front matter name is id or
// name, then any skill whose front matter name is id or name, then a folder
// named id. It answers the folder relative to root with / between the
// parts ("" for the root itself) and whether a skill matched.
func FolderOf(root, id, name string) (string, bool) {
	wantID, wantName := normalName(id), normalName(name)
	named := func(s string) bool {
		n := normalName(s)
		return n != "" && (n == wantID || n == wantName)
	}
	best, rank := "", 0
	walk(root, 0, func(dir string) {
		if rank == 3 {
			return
		}
		s := readSkill(filepath.Join(dir, skillFile), filepath.Base(dir))
		byFolder := wantID != "" && normalName(filepath.Base(dir)) == wantID && dir != root
		r := 0
		switch {
		case byFolder && named(s.Name):
			r = 3
		case named(s.Name):
			r = 2
		case byFolder:
			r = 1
		}
		if r > rank {
			best, rank = dir, r
		}
	})
	if rank == 0 {
		return "", false
	}
	rel, err := filepath.Rel(root, best)
	if err != nil {
		return "", false
	}
	if rel == "." {
		rel = ""
	}
	return filepath.ToSlash(rel), true
}
