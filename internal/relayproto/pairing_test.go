package relayproto

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryPairStore struct {
	mu   sync.Mutex
	slot PairSlot
	fail bool
}

func (s *memoryPairStore) Update(f func(*PairSlot) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("disk failed")
	}
	// Emulate a transaction: discarded when its callback fails.
	b := s.slot.Clone()
	if e := f(&b); e != nil {
		return e
	}
	s.slot = b
	return nil
}
func identityFor(t *testing.T, n byte) *Identity {
	t.Helper()
	seed := make([]byte, 64)
	seed[0] = n
	i, e := ImportIdentity(seed)
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func testRoute() Route                 { return Route{Origin: "https://relay.example", HostID: "office"} }
func fixedRandom(n byte) *bytes.Reader { return bytes.NewReader(bytes.Repeat([]byte{n}, 8192)) }
func TestPairOrderingAndBudget(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := new(memoryPairStore)
	host, e := NewPairHost(testRoute(), identityFor(t, 1), store, func() time.Time { return clock }, fixedRandom(3))
	if e != nil {
		t.Fatal(e)
	}
	ui, commit, e := NewPairUI(testRoute(), identityFor(t, 2), fixedRandom(4))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = host.Commit("connection", commit); e == nil {
		t.Fatal("closed slot replied")
	}
	if e = host.Begin(); e != nil {
		t.Fatal(e)
	}
	reply, e := host.Commit("connection", commit)
	if e != nil || store.slot.Phase != PairCommitted {
		t.Fatal(reply, e, store.slot.Phase)
	}
	reveal, e := ui.Reveal(reply)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ui.Reveal(reply); e == nil {
		t.Fatal("UI reveal reused")
	}
	transcript, e := host.Reveal("connection", reveal)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = transcript.PIN(); e != nil {
		t.Fatal(e)
	}
	if _, e = host.Accept("connection", commit.AID, "000000", nil); e == nil {
		t.Fatal("accepted before possession proof")
	}
	if store.slot.Phase != PairSpent {
		t.Fatal("invalid accept did not burn slot")
	}
	if _, e = host.Commit("new", commit); e == nil {
		t.Fatal("remote rearmed")
	}
}
func TestPairFailuresBurnAndStoragePrecedesReply(t *testing.T) {
	for _, action := range []string{"changed-reveal", "duplicate-commit", "foreign-reveal", "expiry", "disconnect", "restart", "save-failure"} {
		t.Run(action, func(t *testing.T) {
			now := time.Unix(100, 0)
			s := new(memoryPairStore)
			host, _ := NewPairHost(testRoute(), identityFor(t, 1), s, func() time.Time { return now }, fixedRandom(5))
			if e := host.Begin(); e != nil {
				t.Fatal(e)
			}
			ui, c, _ := NewPairUI(testRoute(), identityFor(t, 2), fixedRandom(6))
			if action == "save-failure" {
				s.fail = true
				if r, e := host.Commit("a", c); e == nil || r.Type != "" {
					t.Fatal("reply escaped failed save")
				}
				return
			}
			r, e := host.Commit("a", c)
			if e != nil {
				t.Fatal(e)
			}
			reveal, e := ui.Reveal(r)
			if e != nil {
				t.Fatal(e)
			}
			switch action {
			case "changed-reveal":
				reveal.RU[0] ^= 1
				_, e = host.Reveal("a", reveal)
			case "duplicate-commit":
				_, e = host.Commit("a", c)
			case "foreign-reveal":
				_, e = host.Reveal("b", reveal)
			case "expiry":
				now = now.Add(5 * time.Minute)
				_, e = host.Reveal("a", reveal)
			case "disconnect":
				e = host.Abort()
				if e != nil {
					t.Fatal(e)
				}
				_, e = host.Reveal("a", reveal)
			case "restart":
				e = host.Recover()
				if e != nil {
					t.Fatal(e)
				}
				_, e = host.Reveal("a", reveal)
			}
			if e == nil || s.slot.Phase != PairSpent {
				t.Fatal("failed attempt remained usable", e, s.slot.Phase)
			}
		})
	}
}
func TestPairContributionsFrozen(t *testing.T) {
	ui, c, _ := NewPairUI(testRoute(), identityFor(t, 2), fixedRandom(9))
	hostKey := identityFor(t, 1).PublicKey()
	r := Inner{V: 1, Type: "pair.host", AID: c.AID, Window: make(Bytes, 16), PairEpoch: make(Bytes, 16), PKH: hostKey, RH: make(Bytes, 32)}
	r.AID = bytes.Repeat([]byte{1}, 16)
	if _, e := ui.Reveal(r); e == nil {
		t.Fatal("wrong attempt")
	}
	r.AID = c.AID
	if _, e := ui.Reveal(r); e == nil {
		t.Fatal("failed UI ceremony reused")
	}
}

func TestPairStorageFailureLatchesClosed(t *testing.T) {
	for _, operation := range []string{"begin", "commit", "abort", "recover"} {
		t.Run(operation, func(t *testing.T) {
			s := new(memoryPairStore)
			h, _ := NewPairHost(testRoute(), identityFor(t, 1), s, func() time.Time { return time.Unix(100, 0) }, fixedRandom(5))
			if e := h.Begin(); e != nil {
				t.Fatal(e)
			}
			_, c, _ := NewPairUI(testRoute(), identityFor(t, 2), fixedRandom(6))
			s.fail = true
			var e error
			switch operation {
			case "begin":
				e = h.Begin()
			case "commit":
				_, e = h.Commit("a", c)
			case "abort":
				e = h.Abort()
			case "recover":
				e = h.Recover()
			}
			if e == nil {
				t.Fatal("storage failure ignored")
			}
			s.fail = false
			if r, e := h.Commit("a", c); e == nil || r.Type != "" {
				t.Fatal("storage failure allowed later challenge")
			}
			if e = h.Begin(); e == nil {
				t.Fatal("poisoned host rearmed")
			}
		})
	}
}

func TestConcurrentCommitsHaveOneChallenge(t *testing.T) {
	s := new(memoryPairStore)
	h, _ := NewPairHost(testRoute(), identityFor(t, 1), s, func() time.Time { return time.Unix(100, 0) }, fixedRandom(5))
	if e := h.Begin(); e != nil {
		t.Fatal(e)
	}
	_, c, _ := NewPairUI(testRoute(), identityFor(t, 2), fixedRandom(6))
	var wg sync.WaitGroup
	success := make(chan Inner, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			r, e := h.Commit("a", c)
			if e == nil {
				success <- r
			}
		})
	}
	wg.Wait()
	close(success)
	if len(success) != 1 {
		t.Fatal("challenge budget", len(success))
	}
	if _, e := h.Commit("b", c); e == nil {
		t.Fatal("slot rearmed")
	}
}
