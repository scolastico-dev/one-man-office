// Package branchname turns the branch template of the general settings
// (branches.template) into the name of a ticket's branch, and checks
// templates and names. It is pure apart from Machine, which reads the host
// and user name once.
//
// A template is literal text with placeholders in curly braces: {slug} (the
// ticket title as a slug, "ticket" when the title has no letter or digit),
// {number} (the ticket number), {type} (story or task), {project} (the
// project id as a slug), {host} (this machine's short host name as a slug)
// and {user} (the OS user name as a slug). Every placeholder expands to
// lower-case letters, digits and inner dashes, never to nothing.
package branchname

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Default is the template of a fresh home.
const Default = "omo/{slug}"

// Numbered is the template an older settings file that set
// branches.ticket_number gets.
const Numbered = "omo/{number}-{slug}"

// MaxName is the longest branch name a template may give, with the longest
// value of every placeholder and the longest suffix omo appends.
const MaxName = 200

// MaxSlug is the longest value of {slug}, {project}, {host} and {user}.
const MaxSlug = 40

// maxNumber and maxSuffix are the lengths counted for {number} and for the
// "-<n>" a taken name gets.
const (
	maxNumber = 10
	maxSuffix = 4
)

// Placeholders are the names a template may use, in the order the help
// lists them.
var Placeholders = []string{"slug", "number", "type", "project", "host", "user"}

// Vars are the values of one ticket's placeholders. Title and Project are
// slugged by Expand; Host and User are used as they are (see Machine).
type Vars struct {
	Number  int64
	Title   string
	Type    string
	Project string
	Host    string
	User    string
}

// part is a piece of a parsed template: literal text, or a placeholder.
type part struct {
	text        string
	placeholder bool
}

// Error is a template or name that cannot be used; its message says why
// without quoting anything but placeholder names and example names omo made.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errorf(format string, args ...any) error { return &Error{msg: fmt.Sprintf(format, args...)} }

// known reports whether name is a placeholder.
func known(name string) bool {
	for _, p := range Placeholders {
		if p == name {
			return true
		}
	}
	return false
}

// placeholderList is "{slug}, {number}, ... and {user}".
func placeholderList() string {
	quoted := make([]string, len(Placeholders))
	for i, p := range Placeholders {
		quoted[i] = "{" + p + "}"
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}

// parse splits tmpl into literal text and placeholders.
func parse(tmpl string) ([]part, error) {
	var parts []part
	rest := tmpl
	for rest != "" {
		open := strings.IndexAny(rest, "{}")
		if open < 0 {
			parts = append(parts, part{text: rest})
			break
		}
		if rest[open] == '}' {
			return nil, errorf("the branch template has a } without an opening {")
		}
		if open > 0 {
			parts = append(parts, part{text: rest[:open]})
		}
		end := strings.IndexAny(rest[open+1:], "{}")
		if end < 0 || rest[open+1+end] == '{' {
			return nil, errorf("the branch template has a { without a closing }")
		}
		name := rest[open+1 : open+1+end]
		if !known(name) {
			if plainName(name) {
				return nil, errorf("the branch template uses the unknown placeholder {%s}; it may use %s", name, placeholderList())
			}
			return nil, errorf("the branch template uses an unknown placeholder; it may use %s", placeholderList())
		}
		parts = append(parts, part{text: name, placeholder: true})
		rest = rest[open+2+end:]
	}
	return parts, nil
}

// plainName reports whether s may be quoted in a message as it is.
func plainName(s string) bool {
	if s == "" || len(s) > 30 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// Check reports whether tmpl can be used as the branch template: not empty,
// only known placeholders, {slug} or {number} in it (otherwise every ticket
// would get the same name and only the suffixes would differ), and every
// name it gives a valid git branch name of at most MaxName characters. The
// literal text is checked by expanding the template with a plain value for
// each placeholder. A rare value can still break a name next to the literal
// text (a {user} called refs at the start, a {slug} lock after a dot), so
// Expand checks the name of each ticket again.
func Check(tmpl string) error {
	if tmpl == "" {
		return errorf("the branch template must not be empty")
	}
	parts, err := parse(tmpl)
	if err != nil {
		return err
	}
	unique := false
	for _, p := range parts {
		if p.placeholder && (p.text == "slug" || p.text == "number") {
			unique = true
		}
	}
	if !unique {
		return errorf("the branch template must contain {slug} or {number}; without either every ticket would get the same name")
	}
	if n := len(expand(parts, func(name string) string { return longest(name) })) + maxSuffix; n > MaxName {
		return errorf("the branch template can give names of %d characters; at most %d are allowed", n, MaxName)
	}
	// The literal text is checked with a plain value for every
	// placeholder; values are lower-case letters, digits and inner dashes,
	// so they cannot add a forbidden character, an empty part or "..".
	name := expand(parts, func(name string) string {
		if name == "number" {
			return "1"
		}
		return "x"
	})
	if why := invalid(name); why != "" {
		return errorf("the branch template does not give a valid git branch name: %s (for example %s)", why, shown(name))
	}
	return nil
}

// longest is the longest value of placeholder name.
func longest(name string) string {
	switch name {
	case "number":
		return strings.Repeat("9", maxNumber)
	case "type":
		return "story"
	}
	return strings.Repeat("x", MaxSlug)
}

// expand joins parts with value giving each placeholder's text.
func expand(parts []part, value func(string) string) string {
	var b strings.Builder
	for _, p := range parts {
		if p.placeholder {
			b.WriteString(value(p.text))
		} else {
			b.WriteString(p.text)
		}
	}
	return b.String()
}

// Expand returns the branch name tmpl gives for v, or an *Error when tmpl
// cannot be used or the name is not a valid branch name.
func Expand(tmpl string, v Vars) (string, error) {
	if err := Check(tmpl); err != nil {
		return "", err
	}
	parts, _ := parse(tmpl)
	name := expand(parts, func(p string) string {
		switch p {
		case "slug":
			return orWord(Slug(v.Title), "ticket")
		case "number":
			return strconv.FormatInt(v.Number, 10)
		case "type":
			return orWord(Slug(v.Type), "ticket")
		case "project":
			return orWord(Slug(v.Project), "project")
		case "host":
			return orWord(Slug(v.Host), "host")
		case "user":
			return orWord(Slug(v.User), "user")
		}
		return ""
	})
	if err := Valid(name); err != nil {
		return "", err
	}
	return name, nil
}

func orWord(s, word string) string {
	if s == "" {
		return word
	}
	return s
}

// Valid reports whether name may be the name of a branch omo creates: what
// git check-ref-format --branch accepts, and not starting with "-", "+" or
// refs/, at most MaxName characters, without hidden characters.
func Valid(name string) error {
	if why := invalid(name); why != "" {
		return errorf("%s is not a valid branch name: %s", shown(name), why)
	}
	return nil
}

// shown is name for a message: as it is when it holds only visible ASCII.
func shown(name string) string {
	for _, r := range name {
		if r <= 0x20 || r >= 0x7f {
			return strconv.QuoteToASCII(name)
		}
	}
	return name
}

// invalid says why name is not a valid branch name, or "".
func invalid(name string) string {
	switch {
	case name == "":
		return "it is empty"
	case len(name) > MaxName:
		return fmt.Sprintf("it is longer than %d characters", MaxName)
	case name == "HEAD" || name == "@":
		return "it is a name git keeps for itself"
	case strings.HasPrefix(name, "refs/"):
		return "it starts with refs/"
	case strings.HasPrefix(name, "-") || strings.HasPrefix(name, "+"):
		return "it starts with - or +"
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/"):
		return "it starts or ends with /"
	case strings.Contains(name, "//"):
		return "it contains //"
	case strings.Contains(name, ".."):
		return "it contains .."
	case strings.Contains(name, "@{"):
		return "it contains @{"
	case strings.HasSuffix(name, "."):
		return "it ends with ."
	}
	for _, r := range name {
		switch {
		case r == ' ':
			return "it contains a space"
		case r < 0x20 || r == 0x7f || textsafe.IsHidden(r):
			return "it contains a control or invisible character"
		case strings.ContainsRune("~^:?*[\\", r):
			return "it contains one of ~ ^ : ? * [ \\"
		}
	}
	for _, c := range strings.Split(name, "/") {
		if strings.HasPrefix(c, ".") {
			return "a part of it starts with ."
		}
		if strings.HasSuffix(c, ".lock") {
			return "a part of it ends with .lock"
		}
	}
	return ""
}

// Slug is s in lower case with every run of characters outside a-z and 0-9
// replaced by one dash, without dashes at either end, and cut to at most
// MaxSlug characters at the last dash within the limit (or exactly at the
// limit when there is none). It is "" when s has no such character.
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
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
	if len(slug) <= MaxSlug {
		return slug
	}
	cut := slug[:MaxSlug]
	if slug[MaxSlug] != '-' {
		if i := strings.LastIndexByte(cut, '-'); i > 0 {
			cut = cut[:i]
		}
	}
	return strings.Trim(cut, "-")
}

var machine struct {
	once       sync.Once
	host, user string
}

// Machine returns this machine's short host name (up to the first dot) and
// the OS user name (after a Windows domain), each "" when unknown. They are
// read once.
func Machine() (host, userName string) {
	machine.once.Do(func() {
		if h, err := os.Hostname(); err == nil {
			h, _, _ = strings.Cut(h, ".")
			machine.host = h
		}
		if u, err := user.Current(); err == nil {
			machine.user = u.Username
		}
		if machine.user == "" {
			machine.user = os.Getenv("USER")
		}
		if machine.user == "" {
			machine.user = os.Getenv("USERNAME")
		}
		if i := strings.LastIndexByte(machine.user, '\\'); i >= 0 {
			machine.user = machine.user[i+1:]
		}
	})
	return machine.host, machine.user
}

// Example is the ticket the settings page shows a template's name for.
func Example(project string) Vars {
	host, userName := Machine()
	return Vars{Number: 42, Title: "Fix the login", Type: "task", Project: project, Host: host, User: userName}
}
