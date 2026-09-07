package plugins

const (
	maxLogRunes = 16 * 1024
	// Command output needs a byte bound while it is still being captured. Lua
	// messages retain the equivalent rune bound below the process boundary.
	maxLogBytes = maxLogRunes
)

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
