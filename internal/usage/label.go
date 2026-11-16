package usage

import (
	"strings"
	"unicode"

	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// MaxLabel is the longest label of a window, in characters.
const MaxLabel = 60

// cleanLabel makes a window's label one short line: invalid UTF-8 becomes
// U+FFFD, every character textsafe.IsHidden names (line breaks, tabs,
// terminal controls, reordering and invisible characters) a space, runs of
// spaces one space, no space at either end, and the text is cut to max
// characters (max < 0: not cut). "" stays "": the window has no label.
func cleanLabel(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		if textsafe.IsHidden(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if max >= 0 {
		if r := []rune(s); len(r) > max {
			s = strings.TrimRightFunc(string(r[:max]), unicode.IsSpace)
		}
	}
	return s
}
