package session

import "io"

// Session logs are the raw PTY stream, which for a full-screen CLI is mostly
// colour and cursor-control escapes. stripWriter removes them so the log is
// readable by a human — and by the smoke alarm, which reads the tail.
type stripWriter struct {
	w     io.Writer
	state stripState
	// pending holds an escape sequence still being consumed across writes.
	pending int
}

type stripState int

const (
	stText    stripState = iota
	stEsc                // just saw ESC
	stCSI                // inside ESC [ … final byte
	stOSC                // inside ESC ] … BEL or ST
	stOSCEsc             // inside OSC, just saw ESC (expecting '\')
	stCharset            // ESC ( / ) / * / + — one more byte to drop
)

// maxEscape bounds how long a sequence may run before we give up and treat
// the bytes as text, so a stray ESC cannot swallow the rest of the stream.
const maxEscape = 512

func newStripWriter(w io.Writer) *stripWriter { return &stripWriter{w: w} }

func (s *stripWriter) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p))
	for _, c := range p {
		if s.state != stText {
			s.pending++
			if s.pending > maxEscape {
				s.state, s.pending = stText, 0 // not a real sequence after all
			}
		}
		switch s.state {
		case stText:
			switch {
			case c == 0x1b:
				s.state, s.pending = stEsc, 0
			case c == '\n' || c == '\t':
				out = append(out, c)
			case c == '\r':
				// Column reset in a TUI redraw; a newline is already emitted
				// by \n, so dropping this avoids blank lines.
			case c < 0x20 || c == 0x7f:
				// Other C0 controls (BEL, backspace, …) carry no text.
			default:
				out = append(out, c)
			}
		case stEsc:
			switch c {
			case '[':
				s.state = stCSI
			case ']':
				s.state = stOSC
			case '(', ')', '*', '+':
				s.state = stCharset
			case 0x1b:
				// Another ESC: restart the sequence.
			default:
				s.state = stText // two-byte escape, both dropped
			}
		case stCSI:
			// Parameter and intermediate bytes, terminated by 0x40–0x7e.
			if c >= 0x40 && c <= 0x7e {
				s.state = stText
			}
		case stOSC:
			switch c {
			case 0x07: // BEL
				s.state = stText
			case 0x1b:
				s.state = stOSCEsc
			}
		case stOSCEsc:
			// ESC \ terminates; anything else means we are still in the OSC.
			if c == '\\' {
				s.state = stText
			} else {
				s.state = stOSC
			}
		case stCharset:
			s.state = stText
		}
	}
	if _, err := s.w.Write(out); err != nil {
		return 0, err
	}
	// Report the full input as written: the caller wrote everything it had,
	// and the dropped bytes are intentional.
	return len(p), nil
}
