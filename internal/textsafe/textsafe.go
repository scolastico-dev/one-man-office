// Package textsafe names the characters omo never shows or prints as they
// are, wherever text from agents, git or other outside sources reaches a
// line of omo's output, a terminal or the board: controls that can steer a
// terminal or break a line, characters that reorder the text around them,
// and invisible characters that hide or smuggle text. The board's
// visibleText (web/src/components/ui/visibleText.ts) escapes exactly this
// set; testdata/hidden.json is the shared list both are tested against.
package textsafe

import "unicode"

// IsHidden reports whether r is one of those characters: every control
// character (C0, DEL and C1, which includes NEL and the 8-bit escape
// introducer CSI), the soft hyphen, the Unicode line and paragraph
// separators, the bidirectional marks, embeddings, overrides and isolates,
// the zero width space, word joiner and invisible operators, the zero width
// no-break space (byte order mark), the deprecated format controls, the
// interlinear annotation characters and the tag characters. The joiners
// U+200C and U+200D are not hidden: emoji and several scripts need them.
func IsHidden(r rune) bool {
	switch {
	case unicode.IsControl(r):
		return true
	case r == 0x00ad, r == 0x061c, r == 0x200b, r == 0x200e, r == 0x200f, r == 0xfeff:
		return true
	case r == 0x2028, r == 0x2029:
		return true
	case r >= 0x202a && r <= 0x202e: // embeddings and overrides
		return true
	case r >= 0x2060 && r <= 0x2064: // word joiner, invisible operators
		return true
	case r >= 0x2066 && r <= 0x206f: // isolates, deprecated format controls
		return true
	case r >= 0xfff9 && r <= 0xfffb: // interlinear annotation
		return true
	case r >= 0xe0000 && r <= 0xe007f: // tags
		return true
	}
	return false
}
