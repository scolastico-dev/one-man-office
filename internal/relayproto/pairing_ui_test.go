package relayproto

import (
	"bytes"
	"testing"
)

func TestPairUISnapshotDoesNotResetCeremony(t *testing.T) {
	u, c, e := NewPairUI(testRoute(), identityFor(t, 2), fixedRandom(9))
	if e != nil {
		t.Fatal(e)
	}
	snapshot := u.Snapshot()
	if !bytes.Equal(snapshot.Transcript.AID, c.AID) || len(snapshot.Transcript.RU) != 32 {
		t.Fatal("cannot persist tentative contribution")
	}
	restored, e := RestorePairUI(snapshot)
	if e != nil {
		t.Fatal(e)
	}
	r := Inner{V: 1, Type: "pair.host", AID: c.AID, Window: make(Bytes, 16), PairEpoch: make(Bytes, 16), PKH: identityFor(t, 1).PublicKey(), RH: make(Bytes, 32)}
	if _, e = restored.Reveal(r); e != nil {
		t.Fatal(e)
	}
	used := restored.Snapshot()
	restored, e = RestorePairUI(used)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = restored.Reveal(r); e == nil {
		t.Fatal("restore permitted another reveal")
	}
	if _, e = restored.Transcript(); e != nil {
		t.Fatal(e)
	}
	snapshot.Transcript.RU[0] ^= 1
	if _, e = RestorePairUI(snapshot); e == nil {
		t.Fatal("corrupted saved contribution")
	}
}
