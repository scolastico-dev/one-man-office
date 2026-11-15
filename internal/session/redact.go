package session

import "bytes"

// RedactedPlaceholder replaces Options.Secret wherever the session hands
// output out or stores it.
const RedactedPlaceholder = "[redacted]"

// redactor replaces a secret in a byte stream that arrives in chunks. A
// chunk may end in the middle of the secret, so the longest tail that could
// still become the secret (at most len(secret)-1 bytes) is kept back until
// the next chunk or flush.
type redactor struct {
	secret  []byte
	pending []byte
	out     []byte
}

func newRedactor(secret string) *redactor {
	if secret == "" {
		return nil
	}
	return &redactor{secret: []byte(secret)}
}

// feed returns the redacted output that is safe to emit now. The returned
// slice is reused by the next call.
func (r *redactor) feed(chunk []byte) []byte {
	if r == nil {
		return chunk
	}
	data := append(r.pending, chunk...)
	r.out = r.out[:0]
	for {
		i := bytes.Index(data, r.secret)
		if i < 0 {
			break
		}
		r.out = append(r.out, data[:i]...)
		r.out = append(r.out, RedactedPlaceholder...)
		data = data[i+len(r.secret):]
	}
	keep := r.partialTail(data)
	r.out = append(r.out, data[:len(data)-keep]...)
	r.pending = append(r.pending[:0:0], data[len(data)-keep:]...)
	return r.out
}

// flush returns whatever was kept back; the stream has ended.
func (r *redactor) flush() []byte {
	if r == nil {
		return nil
	}
	out := r.pending
	r.pending = nil
	return out
}

// partialTail is the length of the longest proper prefix of the secret that
// data ends with.
func (r *redactor) partialTail(data []byte) int {
	for n := min(len(r.secret)-1, len(data)); n > 0; n-- {
		if bytes.Equal(data[len(data)-n:], r.secret[:n]) {
			return n
		}
	}
	return 0
}

// redactString replaces every occurrence of secret in text.
func redactString(text, secret string) string {
	if secret == "" {
		return text
	}
	return string(bytes.ReplaceAll([]byte(text), []byte(secret), []byte(RedactedPlaceholder)))
}
