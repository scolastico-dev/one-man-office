package relayproto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"slices"
	"sync"
)

const MaxRecords uint64 = 1 << 24
const MaxRecordBytes uint64 = 1 << 36

type recordDirection struct {
	aead       cipher.AEAD
	direction  string
	seq, bytes uint64
}

// Session serializes both directions and all stream/control records. It performs
// cryptographic framing only; the one Receive demultiplexer enforces stream and
// ready/authorization state. Any record failure permanently closes the session.
type Session struct {
	mu            sync.Mutex
	transcript    []byte
	send, receive recordDirection
	closed        bool
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrProtocol
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}

// NewSession can be called exactly once per authenticated result, preventing
// another writer from resetting a counter under the same key.
func (a *Authenticated) NewSession() (*Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.sessionCreated {
		return nil, ErrProtocol
	}
	s := &Session{transcript: slices.Clone(a.transcript)}
	out, in := "ui-host", "host-ui"
	if a.role == "host" {
		out, in = in, out
	} else if a.role != "ui" {
		return nil, ErrProtocol
	}
	key := expand(a.prk, EncodeFields([]byte(label+"record-"+out)), 32)
	defer clear(key)
	var e error
	s.send.aead, e = newGCM(key)
	if e != nil {
		return nil, e
	}
	s.send.direction = out
	key2 := expand(a.prk, EncodeFields([]byte(label+"record-"+in)), 32)
	defer clear(key2)
	s.receive.aead, e = newGCM(key2)
	if e != nil {
		return nil, e
	}
	s.receive.direction = in
	a.sessionCreated = true
	return s, nil
}
func (s *Session) aad(direction string, seq uint64) []byte {
	return EncodeFields([]byte(label+"record"), u16(Version), []byte(Suite), s.transcript, []byte(direction), u64(seq))
}
func nonceFor(seq uint64) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], seq)
	return nonce
}
func (s *Session) close()       { s.closed = true; s.send.aead = nil; s.receive.aead = nil }
func (s *Session) Close() error { s.mu.Lock(); defer s.mu.Unlock(); s.close(); return nil }
func (s *Session) Seal(p PlainRecord) (out Inner, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	if s.closed {
		return Inner{}, ErrProtocol
	}
	b, e := EncodePlain(p)
	if e != nil {
		return Inner{}, e
	}
	defer clear(b)
	if s.send.seq >= MaxRecords || uint64(len(b)) > MaxRecordBytes-s.send.bytes {
		return Inner{}, ErrProtocol
	}
	ct := s.send.aead.Seal(nil, nonceFor(s.send.seq), b, s.aad(s.send.direction, s.send.seq))
	out = Inner{V: Version, Type: "record", Session: slices.Clone(s.transcript), Seq: Counter(s.send.seq), Ciphertext: ct}
	if _, e := EncodeInner(out); e != nil {
		return Inner{}, e
	}
	s.send.seq++
	s.send.bytes += uint64(len(b))
	return out, nil
}
func (s *Session) Open(m Inner) (out PlainRecord, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	if s.closed {
		return PlainRecord{}, ErrProtocol
	}
	if _, e := EncodeInner(m); e != nil || m.Type != "record" || !bytes.Equal(m.Session, s.transcript) || uint64(m.Seq) != s.receive.seq || s.receive.seq >= MaxRecords {
		return PlainRecord{}, ErrAuthentication
	}
	size := len(m.Ciphertext) - 16
	if uint64(size) > MaxRecordBytes-s.receive.bytes {
		return PlainRecord{}, ErrAuthentication
	}
	b, e := s.receive.aead.Open(nil, nonceFor(s.receive.seq), m.Ciphertext, s.aad(s.receive.direction, s.receive.seq))
	if e != nil {
		return PlainRecord{}, ErrAuthentication
	}
	defer clear(b)
	p, e := DecodePlain(b)
	if e != nil {
		return PlainRecord{}, ErrProtocol
	}
	// Only authenticated and well-formed plaintext advances the receive state.
	s.receive.seq++
	s.receive.bytes += uint64(len(b))
	return p, nil
}
