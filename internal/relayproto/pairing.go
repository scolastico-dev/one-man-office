package relayproto

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"
)

// PairTranscript contains only the contributions frozen before reveal. Later
// handshake ciphertexts must never influence its PIN.
type PairTranscript struct {
	Route                                       Route
	AID, Window, PairEpoch, C, PKU, RU, PKH, RH Bytes
}

func PairCommitment(route Route, aid, pkU, ru []byte) ([]byte, error) {
	if route.validate() != nil || len(aid) != 16 || len(ru) != 32 || ValidatePublicKey(pkU) != nil {
		return nil, ErrProtocol
	}
	return hashFields([]byte(label+"pair-commit"), u16(Version), []byte(Suite), []byte(route.Origin), []byte(route.HostID), aid, pkU, ru), nil
}
func (p PairTranscript) Encode() ([]byte, error) {
	c, e := PairCommitment(p.Route, p.AID, p.PKU, p.RU)
	if e != nil || len(p.Window) != 16 || len(p.PairEpoch) != 16 || len(p.RH) != 32 || ValidatePublicKey(p.PKH) != nil || !hmac.Equal(c, p.C) {
		return nil, ErrProtocol
	}
	return EncodeFields([]byte(label+"pair-transcript"), u16(Version), []byte(Suite), []byte(p.Route.Origin), []byte(p.Route.HostID), p.AID, p.Window, p.PairEpoch, p.C, p.PKU, p.RU, p.PKH, p.RH), nil
}
func (p PairTranscript) Hash() ([]byte, error) {
	b, e := p.Encode()
	if e != nil {
		return nil, e
	}
	return hashBytes(b), nil
}
func hashBytes(b []byte) []byte { // H over already serialized bytes, without a second E.
	h := sha512.Sum384(b)
	return h[:]
}

// PIN returns exactly six decimal digits. Consumers may display it only after
// the mutual possession proof; this deterministic primitive grants no trust.
func (p PairTranscript) PIN() (string, error) {
	b, e := p.Encode()
	if e != nil {
		return "", e
	}
	for i := uint32(0); ; i++ {
		h := hashFields([]byte(label+"pin"), b, u32(i))
		n := binary.BigEndian.Uint32(h[:4])
		if n < 4294000000 {
			return fmt.Sprintf("%06d", n%1000000), nil
		}
		if i == ^uint32(0) {
			return "", ErrProtocol
		}
	}
}
func (p PairTranscript) Clone() PairTranscript {
	p.AID = slices.Clone(p.AID)
	p.Window = slices.Clone(p.Window)
	p.PairEpoch = slices.Clone(p.PairEpoch)
	p.C = slices.Clone(p.C)
	p.PKU = slices.Clone(p.PKU)
	p.RU = slices.Clone(p.RU)
	p.PKH = slices.Clone(p.PKH)
	p.RH = slices.Clone(p.RH)
	return p
}

// PairUI is a one-shot contribution. Persist the tentative contribution before
// sending Commit; never recreate/reveal it in another automatic ceremony.
type PairUI struct {
	mu         sync.Mutex
	transcript PairTranscript
	used       bool
}

func NewPairUI(route Route, id *Identity, random io.Reader) (*PairUI, Inner, error) {
	if route.validate() != nil || ValidatePublicKey(id.PublicKey()) != nil {
		return nil, Inner{}, ErrProtocol
	}
	if random == nil {
		random = rand.Reader
	}
	p := PairTranscript{Route: route, PKU: id.PublicKey(), AID: make(Bytes, 16), RU: make(Bytes, 32)}
	if _, e := io.ReadFull(random, p.AID); e != nil {
		return nil, Inner{}, e
	}
	if _, e := io.ReadFull(random, p.RU); e != nil {
		return nil, Inner{}, e
	}
	c, e := PairCommitment(route, p.AID, p.PKU, p.RU)
	if e != nil {
		return nil, Inner{}, e
	}
	p.C = c
	return &PairUI{transcript: p}, Inner{V: Version, Type: "pair.commit", AID: slices.Clone(p.AID), C: slices.Clone(p.C)}, nil
}
func (u *PairUI) Reveal(reply Inner) (Inner, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.used {
		return Inner{}, ErrProtocol
	}
	u.used = true
	if _, e := EncodeInner(reply); e != nil || reply.Type != "pair.host" || !bytes.Equal(reply.AID, u.transcript.AID) {
		return Inner{}, ErrProtocol
	}
	u.transcript.Window = slices.Clone(reply.Window)
	u.transcript.PairEpoch = slices.Clone(reply.PairEpoch)
	u.transcript.PKH = slices.Clone(reply.PKH)
	u.transcript.RH = slices.Clone(reply.RH)
	return Inner{V: Version, Type: "pair.reveal", AID: slices.Clone(u.transcript.AID), PKU: slices.Clone(u.transcript.PKU), RU: slices.Clone(u.transcript.RU)}, nil
}
func (u *PairUI) Transcript() (PairTranscript, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, e := u.transcript.Encode(); e != nil {
		return PairTranscript{}, e
	}
	return u.transcript.Clone(), nil
}

type PairPhase string

const (
	PairClosed    PairPhase = "closed"
	PairArmed     PairPhase = "armed"
	PairCommitted PairPhase = "committed"
	PairRevealed  PairPhase = "revealed"
	PairProved    PairPhase = "proved"
	PairAccepted  PairPhase = "accepted"
	PairSpent     PairPhase = "spent"
)

// PairSlot is a host-global durable slot. It is local state, never a wire object.
type PairSlot struct {
	Phase            PairPhase
	Expires          time.Time
	Connection       string
	Transcript       PairTranscript
	NotificationRoot Bytes
}

func (s PairSlot) Clone() PairSlot {
	s.Transcript = s.Transcript.Clone()
	s.NotificationRoot = slices.Clone(s.NotificationRoot)
	return s
}

// PairStore.Update must serialize the whole host, apply f to an isolated copy,
// and return only after atomic durable save. On error it commits nothing. The
// host must fail closed after any storage error and on every unfinished restart.
type PairStore interface {
	Update(func(*PairSlot) error) error
}

// PairAction is released only after storage commits. Acceptance is raw material
// for the consumer's device transaction; it is not an authorization capability.
type PairAction struct {
	Acceptance *PairAcceptance
}
type PairAcceptance struct {
	Transcript       PairTranscript
	NotificationRoot Bytes
}
type PairHost struct {
	mu     sync.Mutex
	failed bool
	route  Route
	id     *Identity
	store  PairStore
	now    func() time.Time
	random io.Reader
}

func NewPairHost(route Route, id *Identity, store PairStore, now func() time.Time, random io.Reader) (*PairHost, error) {
	if route.validate() != nil || ValidatePublicKey(id.PublicKey()) != nil || store == nil || now == nil {
		return nil, ErrProtocol
	}
	if random == nil {
		random = rand.Reader
	}
	return &PairHost{route: route, id: id, store: store, now: now, random: random}, nil
}

// update latches every storage failure. The process must repair storage, build
// a new host and Recover before any pairing traffic can resume.
func (h *PairHost) update(f func(*PairSlot) error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failed {
		return ErrProtocol
	}
	if e := h.store.Update(f); e != nil {
		h.failed = true
		return e
	}
	return nil
}

func (h *PairHost) Begin() error {
	return h.update(func(s *PairSlot) error {
		p := PairTranscript{Route: h.route, PKH: h.id.PublicKey(), Window: make(Bytes, 16)}
		if _, e := io.ReadFull(h.random, p.Window); e != nil {
			return e
		}
		*s = PairSlot{Phase: PairArmed, Expires: h.now().Add(5 * time.Minute), Transcript: p}
		return nil
	})
}

// transition saves a spent slot even when validation fails. Returning validation
// errors from the transaction callback would incorrectly roll back its budget.
func (h *PairHost) transition(f func(*PairSlot) error) error {
	var invalid error
	e := h.update(func(s *PairSlot) error {
		if e := f(s); e != nil {
			s.Phase = PairSpent
			clear(s.NotificationRoot)
			s.NotificationRoot = nil
			invalid = e
		}
		return nil
	})
	if e != nil {
		return e
	}
	return invalid
}
func (h *PairHost) valid(s *PairSlot, phase PairPhase, connection string) bool {
	return s.Phase == phase && h.now().Before(s.Expires) && (phase == PairArmed || s.Connection == connection) && s.Transcript.Route == h.route && bytes.Equal(s.Transcript.PKH, h.id.PublicKey())
}
func (h *PairHost) Commit(connection string, m Inner) (Inner, error) {
	var reply Inner
	e := h.transition(func(s *PairSlot) error {
		if connection == "" || !h.valid(s, PairArmed, connection) {
			return ErrProtocol
		}
		if _, e := EncodeInner(m); e != nil || m.Type != "pair.commit" {
			return ErrProtocol
		}
		p := &s.Transcript
		p.AID = slices.Clone(m.AID)
		p.C = slices.Clone(m.C)
		p.PairEpoch = make(Bytes, 16)
		p.RH = make(Bytes, 32)
		if _, e := io.ReadFull(h.random, p.PairEpoch); e != nil {
			return e
		}
		if _, e := io.ReadFull(h.random, p.RH); e != nil {
			return e
		}
		s.Connection = connection
		s.Phase = PairCommitted
		reply = Inner{V: Version, Type: "pair.host", AID: slices.Clone(p.AID), Window: slices.Clone(p.Window), PairEpoch: slices.Clone(p.PairEpoch), PKH: slices.Clone(p.PKH), RH: slices.Clone(p.RH)}
		return nil
	})
	if e != nil {
		return Inner{}, e
	}
	return reply, nil
}
func (h *PairHost) Reveal(connection string, m Inner) (PairTranscript, error) {
	var p PairTranscript
	e := h.transition(func(s *PairSlot) error {
		if !h.valid(s, PairCommitted, connection) {
			return ErrProtocol
		}
		if _, e := EncodeInner(m); e != nil || m.Type != "pair.reveal" || !bytes.Equal(m.AID, s.Transcript.AID) {
			return ErrProtocol
		}
		s.Transcript.PKU = slices.Clone(m.PKU)
		s.Transcript.RU = slices.Clone(m.RU)
		if _, e := s.Transcript.Encode(); e != nil {
			return e
		}
		s.Phase = PairRevealed
		p = s.Transcript.Clone()
		return nil
	})
	if e != nil {
		return PairTranscript{}, e
	}
	return p, nil
}

// Accept compares exactly one proved slot. Empty pin selects the full raw public
// key option; exactly one method is required. Device persistence/trust gating is
// deliberately left to the host consumer, which must save before local success.
func (h *PairHost) Accept(connection string, aid []byte, pin string, publicKey []byte) (PairAction, error) {
	var action PairAction
	e := h.transition(func(s *PairSlot) error {
		if !h.valid(s, PairProved, connection) || !bytes.Equal(aid, s.Transcript.AID) {
			return ErrAuthentication
		}
		expected, e := s.Transcript.PIN()
		if e != nil {
			return e
		}
		good := pin != "" && len(publicKey) == 0 && hmac.Equal([]byte(pin), []byte(expected)) || pin == "" && len(publicKey) == 1184 && hmac.Equal(publicKey, s.Transcript.PKU)
		if !good {
			return ErrAuthentication
		}
		s.Phase = PairAccepted
		action.Acceptance = &PairAcceptance{Transcript: s.Transcript.Clone(), NotificationRoot: slices.Clone(s.NotificationRoot)}
		return nil
	})
	if e != nil {
		return PairAction{}, e
	}
	return action, nil
}
func (h *PairHost) Abort() error {
	return h.update(func(s *PairSlot) error {
		s.Phase = PairSpent
		clear(s.NotificationRoot)
		s.NotificationRoot = nil
		return nil
	})
}

// Recover is mandatory at startup, before processing messages. Accepted device
// records survive in the consumer's store; unfinished slots never resume.
func (h *PairHost) Recover() error {
	return h.update(func(s *PairSlot) error {
		if s.Phase != PairAccepted && s.Phase != PairClosed {
			s.Phase = PairSpent
			clear(s.NotificationRoot)
			s.NotificationRoot = nil
		}
		return nil
	})
}

// PairUIState is a local tentative contribution for private endpoint storage.
// Persist it before commit and persist Used plus frozen host contributions before
// transmitting reveal. It is never a wire object or usable host trust.
type PairUIState struct {
	Transcript PairTranscript
	Used       bool
}

func (u *PairUI) Snapshot() PairUIState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return PairUIState{Transcript: u.transcript.Clone(), Used: u.used}
}
func RestorePairUI(state PairUIState) (*PairUI, error) {
	p := state.Transcript
	c, e := PairCommitment(p.Route, p.AID, p.PKU, p.RU)
	if e != nil || !hmac.Equal(c, p.C) {
		return nil, ErrProtocol
	}
	if !state.Used && (len(p.Window) != 0 || len(p.PairEpoch) != 0 || len(p.RH) != 0 || len(p.PKH) != 0) {
		return nil, ErrProtocol
	}
	if len(p.PKH) != 0 {
		if _, e = p.Encode(); e != nil {
			return nil, e
		}
	}
	return &PairUI{transcript: p.Clone(), used: state.Used}, nil
}
