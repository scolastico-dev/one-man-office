package relayproto

import (
	"bytes"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/sha512"
	"io"
	"slices"
	"sync"
)

// AuthContext must come from the frozen pairing transcript or the consumer's
// pinned device record. Reconnect never accepts new keys supplied by a peer.
type AuthContext struct {
	Route                         Route
	Mode                          string
	PKH, PKU, PairEpoch, PairHash Bytes
}

func (c AuthContext) Clone() AuthContext {
	c.PKH = slices.Clone(c.PKH)
	c.PKU = slices.Clone(c.PKU)
	c.PairEpoch = slices.Clone(c.PairEpoch)
	c.PairHash = slices.Clone(c.PairHash)
	return c
}
func (c AuthContext) Encode() ([]byte, error) {
	if c.Route.validate() != nil || ValidatePublicKey(c.PKH) != nil || ValidatePublicKey(c.PKU) != nil || len(c.PairEpoch) != 16 {
		return nil, ErrProtocol
	}
	if !(c.Mode == "pair" && len(c.PairHash) == 48 || c.Mode == "reconnect" && len(c.PairHash) == 0) {
		return nil, ErrProtocol
	}
	hf, _ := Fingerprint("host", c.PKH)
	ui, _ := Fingerprint("ui", c.PKU)
	return EncodeFields([]byte(label+"auth-context"), u16(Version), []byte(Suite), []byte(c.Mode), []byte(c.Route.Origin), []byte(c.Route.HostID), []byte(hf), []byte(ui), c.PKH, c.PKU, c.PairEpoch, c.PairHash), nil
}
func (c AuthContext) prefix() string {
	if c.Mode == "pair" {
		return "pair.auth"
	}
	return "auth"
}
func (p PairTranscript) AuthContext() (AuthContext, error) {
	h, e := p.Hash()
	if e != nil {
		return AuthContext{}, e
	}
	return AuthContext{Route: p.Route, Mode: "pair", PKH: slices.Clone(p.PKH), PKU: slices.Clone(p.PKU), PairEpoch: slices.Clone(p.PairEpoch), PairHash: h}, nil
}

// Authenticated is produced only by a verified, one-shot mutual proof. It holds
// ephemeral secret material, not device authority. Close it after deriving a
// session and (for pairing only) the persistent notification root.
type Authenticated struct {
	mu              sync.Mutex
	context         AuthContext
	transcript, prk []byte
	role            string
	closed          bool
	sessionCreated  bool
}

func (a *Authenticated) SessionID() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.transcript)
}
func (a *Authenticated) NotificationRoot() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.context.Mode != "pair" {
		return nil, ErrProtocol
	}
	return expand(a.prk, EncodeFields([]byte(label+"notification-root"), a.context.PairHash), 32), nil
}
func (a *Authenticated) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	clear(a.prk)
	a.prk = nil
	a.closed = true
}

// The injected reader supplies protocol nonces. ML-KEM encapsulation always uses
// the standard library's cryptographic RNG; no deterministic wire switch exists.
type UIHandshake struct {
	mu      sync.Mutex
	context AuthContext
	id      *Identity
	init    Inner
	ssH     []byte
	used    bool
}

func NewUIHandshake(c AuthContext, id *Identity, random io.Reader) (*UIHandshake, Inner, error) {
	if _, e := c.Encode(); e != nil || id == nil || !bytes.Equal(id.PublicKey(), c.PKU) {
		return nil, Inner{}, ErrAuthentication
	}
	if random == nil {
		random = rand.Reader
	}
	nu := make(Bytes, 32)
	if _, e := io.ReadFull(random, nu); e != nil {
		return nil, Inner{}, e
	}
	key, _ := mlkem.NewEncapsulationKey768(c.PKH)
	ss, ct := key.Encapsulate()
	hf, _ := Fingerprint("host", c.PKH)
	ui, _ := Fingerprint("ui", c.PKU)
	init := Inner{V: Version, Type: c.prefix() + ".init", HostFP: hf, UIID: ui, PairEpoch: slices.Clone(c.PairEpoch), NU: nu, CTH: ct}
	h := &UIHandshake{context: c.Clone(), id: id, ssH: ss, init: init}
	// The returned message must not alias the stored transcript.
	b, _ := EncodeInner(init)
	out, _ := DecodeInner(b)
	return h, out, nil
}
func (u *UIHandshake) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.used = true
	clear(u.ssH)
	u.ssH = nil
	u.id = nil
}
func (u *UIHandshake) Finish(challenge Inner) (Inner, *Authenticated, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.used {
		return Inner{}, nil, ErrAuthentication
	}
	u.used = true
	defer func() { clear(u.ssH); u.ssH = nil; u.id = nil }()
	if _, e := EncodeInner(challenge); e != nil || challenge.Type != u.context.prefix()+".challenge" {
		return Inner{}, nil, ErrAuthentication
	}
	ssU, e := u.id.key.Decapsulate(challenge.CTU)
	if e != nil {
		return Inner{}, nil, ErrAuthentication
	}
	defer clear(ssU)
	a := deriveAuth(u.context, u.init, challenge, u.ssH, ssU, "ui")
	expected := hostProof(a.prk, a.transcript)
	if !hmac.Equal(expected, challenge.ProofH) {
		a.Close()
		return Inner{}, nil, ErrAuthentication
	}
	proof := uiProof(a.prk, a.transcript, expected)
	return Inner{V: Version, Type: u.context.prefix() + ".finish", ProofU: proof}, a, nil
}

type HostHandshake struct {
	mu      sync.Mutex
	context AuthContext
	id      *Identity
	random  io.Reader
	phase   uint8
	result  *Authenticated
	proofH  []byte
}

func NewHostHandshake(c AuthContext, id *Identity, random io.Reader) (*HostHandshake, error) {
	if _, e := c.Encode(); e != nil || id == nil || !bytes.Equal(id.PublicKey(), c.PKH) {
		return nil, ErrAuthentication
	}
	if random == nil {
		random = rand.Reader
	}
	return &HostHandshake{context: c.Clone(), id: id, random: random}, nil
}
func (h *HostHandshake) Close() { h.mu.Lock(); defer h.mu.Unlock(); h.close() }
func (h *HostHandshake) close() {
	h.phase = 2
	h.id = nil
	if h.result != nil {
		h.result.Close()
		h.result = nil
	}
	clear(h.proofH)
	h.proofH = nil
}
func (h *HostHandshake) Challenge(init Inner) (out Inner, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	defer func() {
		if err != nil {
			h.close()
		}
	}()
	if h.phase != 0 {
		return Inner{}, ErrAuthentication
	}
	h.phase = 1
	if _, e := EncodeInner(init); e != nil || init.Type != h.context.prefix()+".init" {
		return Inner{}, ErrAuthentication
	}
	hf, _ := Fingerprint("host", h.context.PKH)
	ui, _ := Fingerprint("ui", h.context.PKU)
	if init.HostFP != hf || init.UIID != ui || !bytes.Equal(init.PairEpoch, h.context.PairEpoch) {
		return Inner{}, ErrAuthentication
	}
	ssH, e := h.id.key.Decapsulate(init.CTH)
	if e != nil {
		return Inner{}, ErrAuthentication
	}
	defer clear(ssH)
	nh := make(Bytes, 32)
	if _, e := io.ReadFull(h.random, nh); e != nil {
		return Inner{}, e
	}
	key, _ := mlkem.NewEncapsulationKey768(h.context.PKU)
	ssU, ctU := key.Encapsulate()
	defer clear(ssU)
	out = Inner{V: Version, Type: h.context.prefix() + ".challenge", NH: nh, CTU: ctU}
	h.result = deriveAuth(h.context, init, out, ssH, ssU, "host")
	h.proofH = hostProof(h.result.prk, h.result.transcript)
	out.ProofH = slices.Clone(h.proofH)
	return out, nil
}
func (h *HostHandshake) Finish(finish Inner) (*Authenticated, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.phase != 1 {
		h.close()
		return nil, ErrAuthentication
	}
	if _, e := EncodeInner(finish); e != nil || finish.Type != h.context.prefix()+".finish" || !hmac.Equal(uiProof(h.result.prk, h.result.transcript, h.proofH), finish.ProofU) {
		h.close()
		return nil, ErrAuthentication
	}
	result := h.result
	h.result = nil
	h.close()
	return result, nil
}
func deriveAuth(c AuthContext, init, challenge Inner, ssH, ssU []byte, role string) *Authenticated {
	b, _ := c.Encode()
	t := hashFields([]byte(label+"auth-transcript"), b, init.NU, challenge.NH, init.CTH, challenge.CTU)
	secrets := append(slices.Clone(ssH), ssU...)
	defer clear(secrets)
	prk, e := hkdf.Extract(sha512.New384, secrets, t)
	if e != nil {
		panic(e)
	}
	return &Authenticated{context: c.Clone(), transcript: t, prk: prk, role: role}
}
func hostProof(prk, t []byte) []byte {
	key := expand(prk, EncodeFields([]byte(label+"finish-host")), 32)
	defer clear(key)
	return mac(key, EncodeFields([]byte(label+"host-finished"), t))
}
func uiProof(prk, t, proofH []byte) []byte {
	key := expand(prk, EncodeFields([]byte(label+"finish-ui")), 32)
	defer clear(key)
	return mac(key, EncodeFields([]byte(label+"ui-finished"), t, proofH))
}

// Prove binds a host-verified result to this exact frozen slot before allowing
// local acceptance. A reconnect or UI-side result cannot mark a host slot proved.
func (h *PairHost) Prove(connection string, proof *Authenticated) error {
	return h.transition(func(s *PairSlot) error {
		if !h.valid(s, PairRevealed, connection) || proof == nil {
			return ErrAuthentication
		}
		proof.mu.Lock()
		defer proof.mu.Unlock()
		c, e := s.Transcript.AuthContext()
		if e != nil {
			return e
		}
		b, e := c.Encode()
		if e != nil {
			return e
		}
		pb, e := proof.context.Encode()
		if e != nil || proof.closed || proof.role != "host" || !bytes.Equal(b, pb) {
			return ErrAuthentication
		}
		s.NotificationRoot = expand(proof.prk, EncodeFields([]byte(label+"notification-root"), c.PairHash), 32)
		s.Phase = PairProved
		return nil
	})
}
