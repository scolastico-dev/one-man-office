package board

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Mention names. supervisor stands for the ticket's supervisor.
const mentionSupervisor = "supervisor"

var mentionNames = []string{"developer", "reviewer", "pm", mentionSupervisor, "ceo", "user"}

// mentionRule names which mentions a ticket accepts.
const mentionRule = "a task in a story accepts @developer, @reviewer, @pm, @supervisor, @ceo and @user; " +
	"a standalone task all but @pm; a story all but @developer; @ceo only while the CEO is on"

// ParseMentions returns the role names mentioned in a comment body, in
// lower case, in order of first appearance and each once. A mention is "@"
// followed by one of developer, reviewer, pm, supervisor, ceo or user in any
// case. It does not count inside inline code spans or fenced code blocks,
// after a letter, digit or one of _ . @ / - ` (as in an email address or a
// package name), or when the name runs on into a longer word or path.
func ParseMentions(body string) []string {
	text := blankCode([]rune(body))
	out := []string{}
	for i := 0; i < len(text); i++ {
		if text[i] != '@' || (i > 0 && !mentionMayFollow(text[i-1])) {
			continue
		}
		j := i + 1
		for j < len(text) && text[j] < unicode.MaxASCII && unicode.IsLetter(text[j]) {
			j++
		}
		name := strings.ToLower(string(text[i+1 : j]))
		if !slices.Contains(mentionNames, name) || !mentionEndsAt(text, j) {
			continue
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// mentionMayFollow reports whether a mention may start right after r.
func mentionMayFollow(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return false
	}
	return !strings.ContainsRune("_.@/-`", r)
}

// mentionEndsAt reports whether a mention name may end before text[j]: at
// the end, or before a character that does not continue a word or path. A
// dot ends it unless a letter or digit follows the dot.
func mentionEndsAt(text []rune, j int) bool {
	if j >= len(text) {
		return true
	}
	r := text[j]
	if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_-/", r) {
		return false
	}
	if r == '.' && j+1 < len(text) && (unicode.IsLetter(text[j+1]) || unicode.IsDigit(text[j+1])) {
		return false
	}
	return true
}

// blankCode replaces the contents of fenced code blocks and inline code
// spans with spaces. Line breaks and the backticks delimiting a code span
// stay, so positions and the characters around a mention keep their
// meaning.
func blankCode(text []rune) []rune {
	out := slices.Clone(text)
	// Fenced code blocks: a line of at least three backticks or tildes,
	// indented by at most three spaces, opens a block that a line with at
	// least as many of the same character closes; an unclosed block runs to
	// the end.
	var fence rune
	var fenceLen int
	for start := 0; start < len(out); {
		end := start
		for end < len(out) && out[end] != '\n' {
			end++
		}
		line := out[start:end]
		ch, n, rest := fenceOf(line)
		switch {
		case fence == 0 && n >= 3 && !(ch == '`' && strings.ContainsRune(string(rest), '`')):
			fence, fenceLen = ch, n
			blank(line)
		case fence != 0:
			if ch == fence && n >= fenceLen && strings.TrimSpace(string(rest)) == "" {
				fence = 0
			}
			blank(line)
		}
		start = end + 1
	}

	// Inline code spans: a run of n backticks up to the next run of exactly
	// n backticks within the same paragraph. An unmatched run is literal.
	for i := 0; i < len(out); {
		if out[i] != '`' {
			i++
			continue
		}
		n := runLen(out, i, '`')
		closeAt := -1
		for k := i + n; k < len(out); {
			if out[k] == '`' {
				m := runLen(out, k, '`')
				if m == n {
					closeAt = k
					break
				}
				k += m
				continue
			}
			if out[k] == '\n' && blankLineFollows(out, k+1) {
				break
			}
			k++
		}
		if closeAt < 0 {
			i += n
			continue
		}
		for k := i + n; k < closeAt; k++ {
			if out[k] != '\n' {
				out[k] = ' '
			}
		}
		i = closeAt + n
	}
	return out
}

// fenceOf returns the fence character and run length a line starts with
// (after at most three spaces), and the rest of the line; n is 0 when the
// line does not start with a backtick or tilde run.
func fenceOf(line []rune) (rune, int, []rune) {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0, nil
	}
	ch := line[i]
	n := runLen(line, i, ch)
	return ch, n, line[i+n:]
}

func runLen(text []rune, i int, ch rune) int {
	n := 0
	for i+n < len(text) && text[i+n] == ch {
		n++
	}
	return n
}

// blankLineFollows reports whether the line starting at i holds only spaces
// and tabs (or the text ends there).
func blankLineFollows(text []rune, i int) bool {
	for ; i < len(text) && text[i] != '\n'; i++ {
		if text[i] != ' ' && text[i] != '\t' && text[i] != '\r' {
			return false
		}
	}
	return true
}

func blank(rs []rune) {
	for i := range rs {
		rs[i] = ' '
	}
}

// effectiveSupervisor is the role that supervises t now: a ticket the CEO
// supervises falls to the user while the CEO is off.
func effectiveSupervisor(p Policy, t ticketRow) api.Role {
	if t.Supervisor == api.RoleCEO && !p.CEOEnabled {
		return api.RoleUser
	}
	return t.Supervisor
}

// resolveMention checks that a mentioned name is a role on t and returns
// the role it stands for; @supervisor becomes the ticket's supervisor.
func resolveMention(p Policy, t ticketRow, name string) (api.Role, error) {
	valid := true
	role := api.Role(name)
	switch name {
	case mentionSupervisor:
		return effectiveSupervisor(p, t), nil
	case string(api.RoleDeveloper):
		valid = !t.isStory()
	case string(api.RolePM):
		valid = t.isStory() || t.inStory()
	case string(api.RoleCEO):
		valid = p.CEOEnabled
	case string(api.RoleReviewer), string(api.RoleUser):
	default:
		valid = false
	}
	if !valid {
		e := api.Errorf(api.CodeInvalidMention, "@%s cannot be mentioned on %s: %s", name, describeTicket(t), mentionRule)
		e.Details = map[string]any{"mention": name}
		return "", e
	}
	return role, nil
}

// describeTicket names t with its kind, for error messages.
func describeTicket(t ticketRow) string {
	switch {
	case t.isStory():
		return fmt.Sprintf("story #%d", t.ID)
	case t.inStory():
		return fmt.Sprintf("task #%d in story #%d", t.ID, t.StoryID)
	}
	return fmt.Sprintf("standalone task #%d", t.ID)
}
