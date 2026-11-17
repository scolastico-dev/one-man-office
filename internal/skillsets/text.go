package skillsets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// Text returns the skill set blocks for role: one block per set that
// reaches the role (see ForRole), has a checkout fetched from the set's
// repository and ref (see RecordSource) and has something to say
// to the role (a text or at least one skill), sorted by name, separated by
// a blank line, "" when there is none. A block is
//
//	Skill set <name> (checked out at <path>; read-only, omo updates it):
//	<text>
//	Its skills: when a task fits a skill's description, or someone names the skill, read the skill's file and follow it.
//	- <skill>[ (only when someone asks for it)][: <description>]
//	  <file>
//
// The skills are what Find finds for the set's paths. The text is the set's
// instructions for role, trimmed, with every textsafe character other than
// line breaks and tabs removed (the store refuses them; a hand-edited file
// may still hold them). The result is data for a prompt's Instructions
// field, never a template. Blocks that do not fit into MaxText are left
// out and named at the end.
//
// Text is the one way skill sets reach an agent: the engine gives its
// result to every role's prompt (CEO and export jobs included) whatever
// program the agent runs, and the prompt preview shows the same.
func Text(role string, sets []api.SkillSet, checkout func(name string) (path string, ok bool)) string {
	sorted := append([]api.SkillSet(nil), sets...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	var left []string
	for _, k := range sorted {
		if !ForRole(k, role) {
			continue
		}
		path, ok := checkout(k.Name)
		if !ok || !fromSource(path, k) {
			continue
		}
		text := clean(k.Instructions[role])
		skills := Find(path, k.Paths)
		if text == "" && len(skills) == 0 {
			continue
		}
		block := fmt.Sprintf("Skill set %s (checked out at %s; read-only, omo updates it):", k.Name, clean(path))
		if text != "" {
			block += "\n" + text
		}
		if len(skills) > 0 {
			block += "\n" + skillsIntro
			for _, s := range skills {
				block += "\n- " + s.Name
				if s.OnRequest {
					block += " (only when someone asks for it)"
				}
				if s.Description != "" {
					block += ": " + s.Description
				}
				block += "\n  " + clean(s.File)
			}
		}
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

// skillsIntro heads the list of a block's skills.
const skillsIntro = "Its skills: when a task fits a skill's description, or someone names the skill, read the skill's file and follow it."

// ForRole reports whether the set k reaches the agents of role: it is
// enabled, has a valid name and either names no roles (every role) or
// names role. Every place that gives skill sets to agents filters with it.
func ForRole(k api.SkillSet, role string) bool {
	if !k.Enabled || !namePattern.MatchString(k.Name) {
		return false
	}
	return len(k.Roles) == 0 || slices.Contains(k.Roles, role)
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
