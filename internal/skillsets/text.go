package skillsets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// MaxText bounds what Text returns for one role, in bytes. The settings
// store keeps each set's text for a role at 16 KiB; this bounds the sum of
// all sets, so the prompt stays far below what a render may produce.
const MaxText = 64 << 10

// namePattern is the settings store's name rule for skill sets; a name
// outside it never names a checkout.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Text returns the skill set blocks for role: one block per set that is
// enabled, has text for role and has a checkout, sorted by name, separated
// by a blank line, "" when there is none. A block is
//
//	Skill set <name> (checked out at <path>; read-only, omo updates it):
//	<text>
//
// The text is the set's instructions for role, trimmed, with every
// textsafe character other than line breaks and tabs removed (the store
// refuses them; a hand-edited file may still hold them). The result is
// data for a prompt's Instructions field, never a template. Blocks that do
// not fit into MaxText are left out and named at the end.
func Text(role string, sets []api.SkillSet, checkout func(name string) (path string, ok bool)) string {
	sorted := append([]api.SkillSet(nil), sets...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	var left []string
	for _, k := range sorted {
		if !k.Enabled || !namePattern.MatchString(k.Name) {
			continue
		}
		text := clean(k.Instructions[role])
		if text == "" {
			continue
		}
		path, ok := checkout(k.Name)
		if !ok {
			continue
		}
		block := fmt.Sprintf("Skill set %s (checked out at %s; read-only, omo updates it):\n%s", k.Name, clean(path), text)
		if b.Len()+len(block)+2 > MaxText {
			left = append(left, k.Name)
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(block)
	}
	if len(left) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "Skill sets left out because all texts together are longer than %d KiB: %s.", MaxText>>10, strings.Join(left, ", "))
	}
	return b.String()
}

// Join puts the skill set blocks after the user's own instructions,
// separated by a blank line. Both are trimmed; either may be empty.
func Join(user, skills string) string {
	user, skills = strings.TrimSpace(user), strings.TrimSpace(skills)
	switch {
	case skills == "":
		return user
	case user == "":
		return skills
	}
	return user + "\n\n" + skills
}

// CheckoutIn returns the checkout lookup of Text for the skills folder dir:
// dir/<name> when it is a folder (not a link). An empty dir has no
// checkouts.
func CheckoutIn(dir string) func(name string) (string, bool) {
	return func(name string) (string, bool) {
		if dir == "" || !namePattern.MatchString(name) {
			return "", false
		}
		path := filepath.Join(dir, name)
		fi, err := os.Lstat(path)
		if err != nil || !fi.IsDir() {
			return "", false
		}
		return path, true
	}
}

// clean trims s and removes every textsafe character except line breaks
// and tabs.
func clean(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if textsafe.IsHidden(r) {
			return -1
		}
		return r
	}, s))
}
