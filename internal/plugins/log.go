package plugins

import (
	"io"
	"unicode/utf8"
)

const (
	maxLogRunes = 16 * 1024
	// Command output needs a byte bound while it is still being captured. Lua
	// messages retain the equivalent rune bound below the process boundary.
	maxLogBytes = maxLogRunes
	// Mutable command hooks return replacement event JSON on stdout. Keep that
	// protocol useful for normal payloads without allowing a misbehaving command
	// to retain arbitrary output in the office process.
	maxCommandOutputBytes = 64 * 1024
)

func boundedRuneTail(value string, limit int) string {
	end := len(value)
	start := end
	for count := 0; start > 0 && count < limit; count++ {
		_, size := utf8.DecodeLastRuneInString(value[:start])
		start -= size
	}
	if start > 0 {
		return "…" + value[start:end]
	}
	return value
}

type tailBuffer struct {
	limit int
	data  []byte
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit, data: make([]byte, 0, limit)}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		return written, nil
	}
	if excess := len(b.data) + len(p) - b.limit; excess > 0 {
		copy(b.data, b.data[excess:])
		b.data = b.data[:len(b.data)-excess]
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *tailBuffer) String() string { return string(b.data) }

// boundedBuffer retains a prefix up to limit and continues accepting writes so
// the child process cannot block on a full pipe. Overflow is reported to the
// caller, which must reject the partial value rather than parse it.
type boundedBuffer struct {
	limit    int
	data     []byte
	overflow bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit, data: make([]byte, 0, limit)}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - len(b.data)
	if remaining <= 0 {
		if len(p) > 0 {
			b.overflow = true
		}
		return written, nil
	}
	if len(p) > remaining {
		b.data = append(b.data, p[:remaining]...)
		b.overflow = true
		return written, nil
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *boundedBuffer) Bytes() []byte { return b.data }

func (b *boundedBuffer) Overflowed() bool { return b.overflow }

func commandStdoutWriter(mutable bool) (*boundedBuffer, io.Writer) {
	if !mutable {
		return nil, io.Discard
	}
	output := newBoundedBuffer(maxCommandOutputBytes)
	return output, output
}
