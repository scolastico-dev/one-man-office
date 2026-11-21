package relayproto

import (
	"bytes"
	"crypto/mlkem"
	"crypto/mlkem/mlkemtest"
	"errors"
	"testing"
	"time"
)

func authContext(t *testing.T, mode string) AuthContext {
	t.Helper()
	c := AuthContext{Route: testRoute(), Mode: mode, PKH: identityFor(t, 1).PublicKey(), PKU: identityFor(t, 2).PublicKey(), PairEpoch: bytes.Repeat([]byte{3}, 16)}
	if mode == "pair" {
		c.PairHash = bytes.Repeat([]byte{7}, 48)
	}
	return c
}
func completeAuth(t *testing.T, c AuthContext) (*Authenticated, *Authenticated) {
	t.Helper()
	u, init, e := NewUIHandshake(c, identityFor(t, 2), fixedRandom(10))
	if e != nil {
		t.Fatal(e)
	}
	h, e := NewHostHandshake(c, identityFor(t, 1), fixedRandom(11))
	if e != nil {
		t.Fatal(e)
	}
	challenge, e := h.Challenge(init)
	if e != nil {
		t.Fatal(e)
	}
	finish, ur, e := u.Finish(challenge)
	if e != nil {
		t.Fatal(e)
	}
	hr, e := h.Finish(finish)
	if e != nil {
		t.Fatal(e)
	}
	return ur, hr
}
func TestMutualPossession(t *testing.T) {
	for _, mode := range []string{"pair", "reconnect"} {
		t.Run(mode, func(t *testing.T) {
			c := authContext(t, mode)
			u, h := completeAuth(t, c)
			if !bytes.Equal(u.transcript, h.transcript) || !bytes.Equal(u.prk, h.prk) {
				t.Fatal("key agreement")
			}
			if mode == "pair" {
				un, e := u.NotificationRoot()
				if e != nil {
					t.Fatal(e)
				}
				hn, e := h.NotificationRoot()
				if e != nil || !bytes.Equal(un, hn) {
					t.Fatal("root agreement", e)
				}
			} else if _, e := u.NotificationRoot(); e == nil {
				t.Fatal("reconnect replaced persistent root")
			}
			u2, _ := completeAuth(t, c)
			if bytes.Equal(u.transcript, u2.transcript) || bytes.Equal(u.prk, u2.prk) {
				t.Fatal("session key reuse")
			}
		})
	}
}
func TestPossessionAndContextFailures(t *testing.T) {
	for _, mutation := range []string{"host-proof", "ui-proof", "reflection", "route", "epoch", "mode", "host-key", "ui-key", "second-challenge", "second-finish"} {
		t.Run(mutation, func(t *testing.T) {
			c := authContext(t, "reconnect")
			hc := c.Clone()
			switch mutation {
			case "route":
				hc.Route.HostID = "other"
			case "epoch":
				hc.PairEpoch[0] ^= 1
			case "mode":
				hc.Mode = "pair"
				hc.PairHash = make(Bytes, 48)
			case "host-key":
				hc.PKH = identityFor(t, 4).PublicKey()
			case "ui-key":
				hc.PKU = identityFor(t, 4).PublicKey()
			}
			hostID := identityFor(t, 1)
			if mutation == "host-key" {
				hostID = identityFor(t, 4)
			}
			u, init, e := NewUIHandshake(c, identityFor(t, 2), fixedRandom(10))
			if e != nil {
				t.Fatal(e)
			}
			h, e := NewHostHandshake(hc, hostID, fixedRandom(11))
			if e != nil {
				t.Fatal(e)
			}
			challenge, e := h.Challenge(init)
			if mutation == "epoch" || mutation == "mode" || mutation == "host-key" || mutation == "ui-key" {
				if e == nil {
					t.Fatal("unmatched identity/context init")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if mutation == "second-challenge" {
				if _, e = h.Challenge(init); e == nil {
					t.Fatal("second init")
				}
				if _, e = h.Finish(Inner{}); e == nil {
					t.Fatal("failed handshake revived")
				}
				return
			}
			if mutation == "host-proof" {
				challenge.ProofH[0] ^= 1
			}
			finish, result, e := u.Finish(challenge)
			if mutation == "host-proof" || mutation == "route" {
				if e == nil || result != nil {
					t.Fatal("unproved host yielded keys")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if mutation == "reflection" {
				finish.ProofU = challenge.ProofH
			}
			if mutation == "ui-proof" {
				finish.ProofU[0] ^= 1
			}
			if mutation == "second-finish" {
				if _, _, e = u.Finish(challenge); e == nil {
					t.Fatal("second challenge")
				}
				if _, e = h.Finish(finish); e != nil {
					t.Fatal(e)
				}
				if _, e = h.Finish(finish); e == nil {
					t.Fatal("second finish")
				}
				return
			}
			if r, e := h.Finish(finish); e == nil || r != nil {
				t.Fatal("unproved UI yielded keys")
			}
		})
	}
	// Possessing a public key is insufficient; a different private key cannot be
	// used as the pinned UI or host in the high-level handshake at all.
	c := authContext(t, "reconnect")
	if _, _, e := NewUIHandshake(c, identityFor(t, 9), fixedRandom(1)); e == nil {
		t.Fatal("public-only UI")
	}
	if _, e := NewHostHandshake(c, identityFor(t, 9), fixedRandom(1)); e == nil {
		t.Fatal("public-only host")
	}
	for _, bad := range []AuthContext{{}, {Route: testRoute(), Mode: "other"}, {Route: testRoute(), Mode: "reconnect", PKH: c.PKH, PKU: c.PKU, PairEpoch: make(Bytes, 15)}} {
		if _, _, e := NewUIHandshake(bad, identityFor(t, 2), fixedRandom(1)); e == nil {
			t.Fatal("invalid context")
		}
	}
}
func TestPairProofAndAccept(t *testing.T) {
	for _, full := range []bool{false, true} {
		s := new(memoryPairStore)
		h, _ := NewPairHost(testRoute(), identityFor(t, 1), s, func() time.Time { return time.Unix(100, 0) }, fixedRandom(5))
		if e := h.Begin(); e != nil {
			t.Fatal(e)
		}
		u, c, _ := NewPairUI(testRoute(), identityFor(t, 2), fixedRandom(6))
		reply, e := h.Commit("a", c)
		if e != nil {
			t.Fatal(e)
		}
		reveal, e := u.Reveal(reply)
		if e != nil {
			t.Fatal(e)
		}
		p, e := h.Reveal("a", reveal)
		if e != nil {
			t.Fatal(e)
		}
		context, e := p.AuthContext()
		if e != nil {
			t.Fatal(e)
		}
		_, proof := completeAuth(t, context)
		if e = h.Prove("a", proof); e != nil {
			t.Fatal(e)
		}
		pin, e := p.PIN()
		if e != nil {
			t.Fatal(e)
		}
		var pk []byte
		if full {
			pk = p.PKU
			pin = ""
		}
		action, e := h.Accept("a", p.AID, pin, pk)
		if e != nil || action.Acceptance == nil || len(action.Acceptance.NotificationRoot) != 32 || s.slot.Phase != PairAccepted {
			t.Fatal(action, e)
		}
		if _, e = h.Accept("a", p.AID, pin, pk); e == nil {
			t.Fatal("repeated acceptance")
		}
	}
}

func TestProvedPairRejectsWrongOrReusedAcceptance(t *testing.T) {
	for _, mutation := range []string{"wrong-pin", "wrong-key", "duplicate-reveal", "wrong-proof", "ui-result", "disconnected"} {
		t.Run(mutation, func(t *testing.T) {
			s := new(memoryPairStore)
			h, _ := NewPairHost(testRoute(), identityFor(t, 1), s, func() time.Time { return time.Unix(100, 0) }, fixedRandom(5))
			if e := h.Begin(); e != nil {
				t.Fatal(e)
			}
			u, c, _ := NewPairUI(testRoute(), identityFor(t, 2), fixedRandom(6))
			r, e := h.Commit("a", c)
			if e != nil {
				t.Fatal(e)
			}
			reveal, e := u.Reveal(r)
			if e != nil {
				t.Fatal(e)
			}
			p, e := h.Reveal("a", reveal)
			if e != nil {
				t.Fatal(e)
			}
			ac, e := p.AuthContext()
			if e != nil {
				t.Fatal(e)
			}
			ui, proof := completeAuth(t, ac)
			switch mutation {
			case "wrong-proof":
				_, proof = completeAuth(t, authContext(t, "pair"))
				if e = h.Prove("a", proof); e == nil {
					t.Fatal("foreign proof")
				}
			case "ui-result":
				if e = h.Prove("a", ui); e == nil {
					t.Fatal("UI-side proof used by host")
				}
			case "duplicate-reveal":
				if _, e = h.Reveal("a", reveal); e == nil {
					t.Fatal("second reveal")
				}
			default:
				if e = h.Prove("a", proof); e != nil {
					t.Fatal(e)
				}
				var key []byte
				pin, _ := p.PIN()
				switch mutation {
				case "wrong-pin":
					if pin[0] == '0' {
						pin = "1" + pin[1:]
					} else {
						pin = "0" + pin[1:]
					}
				case "wrong-key":
					pin = ""
					key = identityFor(t, 8).PublicKey()
				case "disconnected":
					if e = h.Abort(); e != nil {
						t.Fatal(e)
					}
				}
				if _, e = h.Accept("a", p.AID, pin, key); e == nil {
					t.Fatal("incorrect acceptance")
				}
			}
			if s.slot.Phase != PairSpent {
				t.Fatal("failed attempt not spent")
			}
		})
	}
}

// An attacker can encapsulate to the UI public key, but cannot recover ssH
// from the UI's challenge to the pinned host key. Dropping ssH from the PRK
// would turn this correctly shaped forged proof into a successful handshake.
func TestHostPublicKeyWithoutPrivatePossessionFails(t *testing.T) {
	c := authContext(t, "reconnect")
	u, init, e := NewUIHandshake(c, identityFor(t, 2), fixedRandom(10))
	if e != nil {
		t.Fatal(e)
	}
	defer u.Close()
	wrongSS, e := identityFor(t, 9).key.Decapsulate(init.CTH)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(wrongSS)
	uiKey, e := mlkem.NewEncapsulationKey768(c.PKU)
	if e != nil {
		t.Fatal(e)
	}
	knownSS, ctU, e := mlkemtest.Encapsulate768(uiKey, bytes.Repeat([]byte{12}, 32))
	if e != nil {
		t.Fatal(e)
	}
	defer clear(knownSS)
	challenge := Inner{V: Version, Type: "auth.challenge", NH: bytes.Repeat([]byte{11}, 32), CTU: ctU}
	forged := deriveAuth(c, init, challenge, wrongSS, knownSS, "host")
	defer forged.Close()
	challenge.ProofH = hostProof(forged.prk, forged.transcript)
	wire, e := EncodeInner(challenge)
	if e != nil {
		t.Fatal(e)
	}
	challenge, e = DecodeInner(wire)
	if e != nil {
		t.Fatal(e)
	}
	finish, result, e := u.Finish(challenge)
	if !errors.Is(e, ErrAuthentication) || result != nil || finish.Type != "" {
		t.Fatal("host without pinned private key authenticated", e)
	}
}

// The mirror attacker knows ssH from its own encapsulation to the host, but
// cannot recover ssU from the host challenge to the pinned UI key. Dropping
// ssU from the PRK would let it produce an accepted, well-formed UI proof.
func TestUIPublicKeyWithoutPrivatePossessionFails(t *testing.T) {
	c := authContext(t, "reconnect")
	hostKey, e := mlkem.NewEncapsulationKey768(c.PKH)
	if e != nil {
		t.Fatal(e)
	}
	knownSS, ctH, e := mlkemtest.Encapsulate768(hostKey, bytes.Repeat([]byte{10}, 32))
	if e != nil {
		t.Fatal(e)
	}
	defer clear(knownSS)
	hf, e := Fingerprint("host", c.PKH)
	if e != nil {
		t.Fatal(e)
	}
	ui, e := Fingerprint("ui", c.PKU)
	if e != nil {
		t.Fatal(e)
	}
	init := Inner{V: Version, Type: "auth.init", HostFP: hf, UIID: ui, PairEpoch: c.PairEpoch, NU: bytes.Repeat([]byte{9}, 32), CTH: ctH}
	h, e := NewHostHandshake(c, identityFor(t, 1), fixedRandom(11))
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	challenge, e := h.Challenge(init)
	if e != nil {
		t.Fatal(e)
	}
	wrongSS, e := identityFor(t, 9).key.Decapsulate(challenge.CTU)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(wrongSS)
	forged := deriveAuth(c, init, challenge, knownSS, wrongSS, "ui")
	defer forged.Close()
	finish := Inner{V: Version, Type: "auth.finish", ProofU: uiProof(forged.prk, forged.transcript, challenge.ProofH)}
	wire, e := EncodeInner(finish)
	if e != nil {
		t.Fatal(e)
	}
	finish, e = DecodeInner(wire)
	if e != nil {
		t.Fatal(e)
	}
	if result, e := h.Finish(finish); !errors.Is(e, ErrAuthentication) || result != nil {
		t.Fatal("UI without pinned private key authenticated", e)
	}
}
