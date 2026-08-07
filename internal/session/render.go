package session

import (
	"strconv"
	"strings"

	"github.com/hinshun/vt10x"
)

// vt10x attribute bits (unexported upstream, stable in the pinned version).
const (
	attrReverse = 1 << iota
	attrUnderline
	attrBold
	attrGfx
	attrItalic
	attrBlink
)

// vt10x packs 24-bit colours as r<<16|g<<8|b, so anything at or above 256 and
// below 1<<24 is truecolor; below 256 is an ANSI/xterm palette index.
const trueColorMin = 256

type cellStyle struct {
	fg, bg              vt10x.Color
	bold, underline     bool
	italic, reverse     bool
	blink, styleIsUnset bool
}

// ScreenANSI renders the virtual terminal with its colours and attributes, so
// the TUI's peek view looks like the agent's real terminal. Screen() returns
// the same content as plain text.
func (s *Session) ScreenANSI() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cols, rows := s.term.Size()
	var b strings.Builder
	cur := cellStyle{styleIsUnset: true}
	for y := 0; y < rows; y++ {
		// Find the last non-blank cell so we do not paint whole blank rows.
		last := -1
		for x := 0; x < cols; x++ {
			g := s.term.Cell(x, y)
			if g.Char != ' ' && g.Char != 0 || styleOf(g) != (cellStyle{fg: vt10x.DefaultFG, bg: vt10x.DefaultBG}) {
				if g.Char != 0 && g.Char != ' ' {
					last = x
				}
			}
		}
		for x := 0; x <= last; x++ {
			g := s.term.Cell(x, y)
			st := styleOf(g)
			if cur.styleIsUnset || st != cur {
				b.WriteString(sgrFor(st))
				cur = st
			}
			ch := g.Char
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		if y < rows-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

func styleOf(g vt10x.Glyph) cellStyle {
	return cellStyle{
		fg:        g.FG,
		bg:        g.BG,
		bold:      g.Mode&attrBold != 0,
		underline: g.Mode&attrUnderline != 0,
		italic:    g.Mode&attrItalic != 0,
		reverse:   g.Mode&attrReverse != 0,
		blink:     g.Mode&attrBlink != 0,
	}
}

// sgrFor emits a full SGR sequence for a style (reset first, so no attribute
// from a previous cell can leak into this one).
func sgrFor(st cellStyle) string {
	parts := []string{"0"}
	if st.bold {
		parts = append(parts, "1")
	}
	if st.italic {
		parts = append(parts, "3")
	}
	if st.underline {
		parts = append(parts, "4")
	}
	if st.blink {
		parts = append(parts, "5")
	}
	if st.reverse {
		parts = append(parts, "7")
	}
	parts = append(parts, colorSGR(st.fg, true)...)
	parts = append(parts, colorSGR(st.bg, false)...)
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

// colorSGR renders one colour as SGR parameters. fg selects the 3x/9x/38
// family over the 4x/10x/48 one.
func colorSGR(c vt10x.Color, fg bool) []string {
	base, ext := 30, "38"
	if !fg {
		base, ext = 40, "48"
	}
	switch {
	case c == vt10x.DefaultFG || c == vt10x.DefaultBG || c >= 1<<24:
		return nil // leave at the reset default
	case c < 8:
		return []string{strconv.Itoa(base + int(c))}
	case c < 16:
		return []string{strconv.Itoa(base + 60 + int(c-8))} // bright
	case c < trueColorMin:
		return []string{ext, "5", strconv.Itoa(int(c))}
	default:
		r, g, bl := (c>>16)&0xff, (c>>8)&0xff, c&0xff
		return []string{ext, "2", strconv.Itoa(int(r)), strconv.Itoa(int(g)), strconv.Itoa(int(bl))}
	}
}
