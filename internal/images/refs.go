package images

import (
	"regexp"
	"strings"
)

// MaxRefs bounds how many different images one text is annotated for; a
// reference beyond it is left as written.
const MaxRefs = 100

// Ref is one Markdown image reference to an uploaded image in a text.
type Ref struct {
	// Alt is the alt text with its backslash escapes undone: the name the
	// image was uploaded with, unless the writer changed it.
	Alt string
	ID  string
	// Start and End are the byte offsets of the whole reference.
	Start, End int
}

// refPattern matches ![alt](omo-image:<uuid>): the alt text may hold escaped
// characters but no unescaped bracket or line break.
var refPattern = regexp.MustCompile(`!\[((?:[^\]\[\\\n]|\\[^\n])*)\]\(omo-image:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\)`)

// Refs lists the image references in text, in order.
func Refs(text string) []Ref {
	var out []Ref
	for _, m := range refPattern.FindAllStringSubmatchIndex(text, -1) {
		out = append(out, Ref{
			Alt:   unescapeAlt(text[m[2]:m[3]]),
			ID:    text[m[4]:m[5]],
			Start: m[0],
			End:   m[1],
		})
	}
	return out
}

func unescapeAlt(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	escaped := false
	for _, r := range s {
		if r == '\\' && !escaped {
			escaped = true
			continue
		}
		escaped = false
		b.WriteRune(r)
	}
	return b.String()
}

// Note is what Annotate says after a reference: the local path of a stored
// image, or why there is none.
type Note struct {
	Path    string // absolute path of the stored file
	Expired bool
	Missing bool // no record, no file, or nothing the reader may see
}

// Annotate returns text with a note after every image reference: "(local
// file: <path>)" for an image whose file is stored, "(image expired)" or
// "(image missing)" otherwise. look is asked once per different id (at most
// MaxRefs ids; later ones are left as written). The reference itself stays,
// so the reader still sees the original name.
func Annotate(text string, look func(id string) Note) string {
	refs := Refs(text)
	if len(refs) == 0 {
		return text
	}
	notes := map[string]string{}
	var b strings.Builder
	last := 0
	for _, r := range refs {
		note, seen := notes[r.ID]
		if !seen {
			if len(notes) >= MaxRefs {
				continue
			}
			n := look(r.ID)
			switch {
			case n.Path != "":
				note = " (local file: " + n.Path + ")"
			case n.Expired:
				note = " (image expired)"
			default:
				note = " (image missing)"
			}
			notes[r.ID] = note
		}
		b.WriteString(text[last:r.End])
		b.WriteString(note)
		last = r.End
	}
	b.WriteString(text[last:])
	return b.String()
}

// ForExport replaces every image reference by "[image: <name>, not
// exported]": a pull request cannot show an image kept in the omo home.
func ForExport(text string) string {
	refs := Refs(text)
	if len(refs) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, r := range refs {
		b.WriteString(text[last:r.Start])
		b.WriteString("[image: " + escapeAlt(strings.TrimSpace(r.Alt)) + ", not exported]")
		last = r.End
	}
	b.WriteString(text[last:])
	return b.String()
}
