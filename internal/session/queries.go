package session

import "bytes"

// omo is the terminal emulator behind every agent PTY, so it has to behave
// like one: programs built on termenv/bubbletea — including omo's own CLI
// verbs, which link the TUI — probe the terminal at startup for its
// foreground/background colours and cursor position, then block on a five
// second timeout when nothing replies. Answering keeps every in-session
// `omo inbox`/`omo done` instant.
var (
	queryDSR = []byte("\x1b[6n")  // device status report: cursor position
	queryFG  = []byte("\x1b]10;") // OSC 10: foreground colour
	queryBG  = []byte("\x1b]11;") // OSC 11: background colour

	replyDSR = []byte("\x1b[1;1R")
	replyFG  = []byte("\x1b]10;rgb:ffff/ffff/ffff\x1b\\")
	replyBG  = []byte("\x1b]11;rgb:0000/0000/0000\x1b\\")
)

// maxCarry bounds the partial-sequence buffer: no query we answer is longer.
const maxCarry = 16

// answerQueries scans terminal output for device queries, writes the replies
// back into the PTY and returns any trailing bytes that may be the start of a
// query split across reads (to be prepended to the next chunk).
func (s *Session) answerQueries(chunk []byte) []byte {
	for {
		i := bytes.IndexByte(chunk, 0x1b)
		if i < 0 {
			return nil // no escape in flight — nothing worth carrying
		}
		rest := chunk[i:]
		reply, consumed, partial := matchQuery(rest)
		if partial {
			// Might become a query once more bytes arrive. Cap the carry so a
			// lone ESC in ordinary output cannot grow without bound.
			if len(rest) > maxCarry {
				chunk = rest[1:]
				continue
			}
			return rest
		}
		if reply != nil {
			s.sendInput(reply)
			chunk = rest[consumed:]
			continue
		}
		chunk = rest[1:] // an escape sequence we do not answer
	}
}

// matchQuery inspects a slice starting at ESC. It returns the reply and the
// number of bytes the query occupies, or partial=true when the slice could
// still grow into a query we answer.
func matchQuery(b []byte) (reply []byte, consumed int, partial bool) {
	if p, full := hasPrefix(b, queryDSR); full {
		return replyDSR, len(queryDSR), false
	} else if p {
		return nil, 0, true
	}
	for _, q := range []struct {
		prefix []byte
		reply  []byte
	}{{queryBG, replyBG}, {queryFG, replyFG}} {
		p, full := hasPrefix(b, q.prefix)
		if !full {
			if p {
				return nil, 0, true
			}
			continue
		}
		// OSC colour queries are "<prefix>?<terminator>", terminated by BEL
		// or ST (ESC \). Anything else on this OSC is a set, not a query.
		body := b[len(q.prefix):]
		end, ok, more := oscEnd(body)
		if more {
			return nil, 0, true
		}
		if !ok || string(body[:end]) != "?" {
			return nil, 0, false
		}
		return q.reply, len(q.prefix) + oscConsumed(body, end), false
	}
	return nil, 0, false
}

// hasPrefix reports whether b starts with prefix (full) or is a proper
// prefix of it (partial).
func hasPrefix(b, prefix []byte) (partial, full bool) {
	if len(b) >= len(prefix) {
		return false, bytes.Equal(b[:len(prefix)], prefix)
	}
	return bytes.Equal(b, prefix[:len(b)]), false
}

// oscEnd finds the terminator of an OSC body, reporting more=true when the
// terminator has not arrived yet.
func oscEnd(body []byte) (end int, ok bool, more bool) {
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case 0x07: // BEL
			return i, true, false
		case 0x1b:
			if i+1 >= len(body) {
				return 0, false, true // ESC could still be followed by '\'
			}
			if body[i+1] == '\\' {
				return i, true, false
			}
			return i, false, false
		}
		if i > maxCarry {
			return 0, false, false // too long to be a colour query
		}
	}
	return 0, false, true
}

// oscConsumed returns the byte length of an OSC body including terminator.
func oscConsumed(body []byte, end int) int {
	if body[end] == 0x07 {
		return end + 1
	}
	return end + 2 // ESC \
}

// sendInput writes bytes into the PTY as if typed. Tests substitute
// writeInput to observe replies without a real PTY.
func (s *Session) sendInput(b []byte) {
	if s.writeInput != nil {
		s.writeInput(b)
		return
	}
	s.proc.Write(b)
}
