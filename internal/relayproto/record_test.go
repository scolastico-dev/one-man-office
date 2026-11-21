package relayproto

import (
	"bytes"
	"slices"
	"sync"
	"testing"
)

func sessions(t *testing.T) (*Session, *Session) {
	t.Helper()
	u, h := completeAuth(t, authContext(t, "reconnect"))
	us, e := u.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	hs, e := h.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	u.Close()
	h.Close()
	return us, hs
}
func TestRecordsAndDirection(t *testing.T) {
	u, h := sessions(t)
	for _, p := range []PlainRecord{{V: 1, Type: "stream.open", ID: 1}, {V: 1, Type: "stream.data", ID: 1, Data: Bytes("hello")}, {V: 1, Type: "stream.fin", ID: 1}} {
		r, e := u.Seal(p)
		if e != nil {
			t.Fatal(e)
		}
		got, e := h.Open(r)
		if e != nil || got.Type != p.Type || !bytes.Equal(got.Data, p.Data) {
			t.Fatal(got, e)
		}
	}
	r, e := h.Seal(PlainRecord{V: 1, Type: "stream.opened", ID: 1})
	if e != nil || r.Seq != 0 {
		t.Fatal(r, e)
	}
	if _, e = u.Open(r); e != nil {
		t.Fatal(e)
	}
	if u.send.seq != 3 || h.receive.seq != 3 {
		t.Fatal("global sequence")
	}
}
func TestRecordFailuresTerminateSession(t *testing.T) {
	for _, mutation := range []string{"replay", "gap", "reflection", "tag", "session", "aad", "version", "type", "cross-session", "invalid-json"} {
		t.Run(mutation, func(t *testing.T) {
			u, h := sessions(t)
			r, e := u.Seal(PlainRecord{V: 1, Type: "stream.open", ID: 1})
			if e != nil {
				t.Fatal(e)
			}
			target := h
			switch mutation {
			case "replay":
				if _, e = h.Open(r); e != nil {
					t.Fatal(e)
				}
			case "gap":
				r.Seq = 1
			case "reflection":
				target = u
			case "tag":
				r.Ciphertext[len(r.Ciphertext)-1] ^= 1
			case "session":
				r.Session[0] ^= 1
			case "aad":
				h.receive.direction = "ui-ui"
			case "version":
				r.V = 2
			case "type":
				r.Type = "auth.init"
			case "cross-session":
				_, target = sessions(t)
			case "invalid-json":
				nonce := make([]byte, 12)
				r.Ciphertext = u.send.aead.Seal(nil, nonce, []byte(`{"v":1,"type":"stream.open","id":"1","id":"3"}`), u.aad("ui-host", 0))
			}
			if _, e = target.Open(r); e == nil {
				t.Fatal("accepted", mutation)
			}
			if _, e = target.Seal(PlainRecord{V: 1, Type: "stream.open", ID: 3}); e == nil {
				t.Fatal("failed session reused")
			}
		})
	}
}
func TestSessionLimitsAndConcurrentSend(t *testing.T) {
	for _, kind := range []string{"records", "bytes"} {
		u, h := sessions(t)
		if kind == "records" {
			u.send.seq = MaxRecords
			h.receive.seq = MaxRecords
		} else {
			u.send.bytes = MaxRecordBytes
			h.receive.bytes = MaxRecordBytes
		}
		if _, e := u.Seal(PlainRecord{V: 1, Type: "stream.open", ID: 1}); e == nil {
			t.Fatal("send limit")
		}
		fresh, _ := sessions(t)
		r, e := fresh.Seal(PlainRecord{V: 1, Type: "stream.open", ID: 1})
		if e != nil {
			t.Fatal(e)
		}
		// Use the matching original peer material to exercise receive limits, not a
		// mismatched session or tag failure.
		other, peer := sessions(t)
		r, e = other.Seal(PlainRecord{V: 1, Type: "stream.open", ID: 1})
		if e != nil {
			t.Fatal(e)
		}
		if kind == "records" {
			peer.receive.seq = MaxRecords
			r.Seq = Counter(MaxRecords)
		} else {
			peer.receive.bytes = MaxRecordBytes
		}
		if _, e = peer.Open(r); e == nil {
			t.Fatal("receive limit")
		}
	}
	u, h := sessions(t)
	var wg sync.WaitGroup
	records := make(chan Inner, 32)
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			r, e := u.Seal(PlainRecord{V: 1, Type: "stream.open", ID: 1})
			if e != nil {
				errs <- e
			} else {
				records <- r
			}
		})
	}
	wg.Wait()
	close(records)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	var sorted []Inner
	for r := range records {
		sorted = append(sorted, r)
	}
	slices.SortFunc(sorted, func(a, b Inner) int {
		if a.Seq < b.Seq {
			return -1
		}
		if a.Seq > b.Seq {
			return 1
		}
		return 0
	})
	for i, r := range sorted {
		if uint64(r.Seq) != uint64(i) {
			t.Fatal("nonce reused")
		}
		if _, e := h.Open(r); e != nil {
			t.Fatal(e)
		}
	}
}
func TestAuthenticatedMaterialLifecycle(t *testing.T) {
	u, _ := completeAuth(t, authContext(t, "pair"))
	s, e := u.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = u.NewSession(); e == nil {
		t.Fatal("same material started another counter at zero")
	}
	u.Close()
	if _, e = u.NotificationRoot(); e == nil {
		t.Fatal("closed keys")
	}
	if _, e = u.NewSession(); e == nil {
		t.Fatal("closed session keys")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Seal(PlainRecord{V: 1, Type: "stream.open", ID: 1}); e == nil {
		t.Fatal("closed session")
	}
}
