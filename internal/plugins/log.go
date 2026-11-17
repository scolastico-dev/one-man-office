package plugins

import (
	"strings"
	"unicode/utf8"
)

// Bounds of what a hook's run leaves behind.
const (
	// maxOutputBytes bounds what omo keeps of a program's standard output
	// (a command hook, omo.exec).
	maxOutputBytes = 1 << 20
	// maxStderrBytes bounds what omo keeps of a command hook's standard
	// error: the tail, where the reason of a failure usually is.
	maxStderrBytes = 64 << 10
	// maxLogLineRunes bounds one line of a plugin's log.
	maxLogLineRunes = 500
	// maxLogLinesPerRun bounds the lines one run adds to the log.
	maxLogLinesPerRun = 50
	// maxMessageRunes bounds the answer of a manual action shown to the
	// user.
	maxMessageRunes = 1000
	// maxErrorRunes bounds an error text kept for a plugin.
	maxErrorRunes = 300
)

// cutRunes cuts s to at most n characters, marking a cut with "…".
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n-1 {
			return s[:i] + "…"
		}
		count++
	}
	return s
}

// splitLines splits text into its non-empty lines: at most
// maxLogLinesPerRun of them (the last ones).
func splitLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	if len(out) > maxLogLinesPerRun {
		out = out[len(out)-maxLogLinesPerRun:]
	}
	return out
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	limit int
	data  []byte
}

func newTailBuffer(limit int) *tailBuffer { return &tailBuffer{limit: limit} }

func (b *tailBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		return written, nil
	}
	if excess := len(b.data) + len(p) - b.limit; excess > 0 {
		b.data = append(b.data[:0], b.data[excess:]...)
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *tailBuffer) String() string { return string(b.data) }

// headBuffer keeps the first limit bytes written to it and accepts (and
// drops) the rest, so a program never blocks on a full pipe.
type headBuffer struct {
	limit    int
	data     []byte
	overflow bool
}

func newHeadBuffer(limit int) *headBuffer { return &headBuffer{limit: limit} }

func (b *headBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if room := b.limit - len(b.data); len(p) > room {
		p = p[:max(room, 0)]
		b.overflow = true
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *headBuffer) Bytes() []byte { return b.data }
