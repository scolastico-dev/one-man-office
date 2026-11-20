package relayhost

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

func managerForTest(t *testing.T) (*Manager, *Store) {
	t.Helper()
	s, e := OpenStore(filepath.Join(t.TempDir(), "relay"))
	if e != nil {
		t.Fatal(e)
	}
	m, e := NewManager(s, Options{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close() })
	return m, s
}
func TestManagerConnectRestartDisabledAndRandomID(t *testing.T) {
	m, s := managerForTest(t)
	if err := m.Connect(ConnectOptions{Origin: "https://relay.example", ClientID: "office"}); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Snapshot()
	if len(first.Seed) != 64 || first.HostID != "office" {
		t.Fatal("identity not persisted")
	}
	if err := m.Disconnect(); err != nil {
		t.Fatal(err)
	}
	m2, e := NewManager(s, Options{Origin: first.Origin, Enabled: false})
	if e != nil {
		t.Fatal(e)
	}
	defer m2.Close()
	if m2.Status().State != "disabled" {
		t.Fatal(m2.Status())
	}
	if err := m2.Connect(ConnectOptions{Origin: first.Origin}); err != nil {
		t.Fatal(err)
	}
	second, _ := s.Snapshot()
	if second.HostID != first.HostID || !bytes.Equal(first.Seed, second.Seed) {
		t.Fatal("restart replaced identity")
	}
	if err := m2.Connect(ConnectOptions{Origin: first.Origin, NewID: true}); err != nil {
		t.Fatal(err)
	}
	third, _ := s.Snapshot()
	if len(third.HostID) != 22 || third.HostID == first.HostID || !bytes.Equal(first.Seed, third.Seed) {
		t.Fatal("new-id changed key or id invalid")
	}
	missing, _ := OpenStore(filepath.Join(t.TempDir(), "relay"))
	paused, e := NewManager(missing, Options{Origin: first.Origin, Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	defer paused.Close()
	if paused.Status().State != "missing_state" {
		t.Fatal(paused.Status())
	}
}
func TestManagerPairSlotExpiryAndRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, s := managerForTest(t)
		if e := m.Connect(ConnectOptions{Origin: "https://relay.example"}); e != nil {
			t.Fatal(e)
		}
		if e := m.PairBegin(); e != nil {
			t.Fatal(e)
		}
		v, _ := s.Snapshot()
		if v.Pair.Phase != relayproto.PairArmed {
			t.Fatal(v.Pair.Phase)
		}
		// Constructor recovery burns even an unused armed slot.
		next, e := NewManager(s, Options{Origin: v.Origin, Enabled: true})
		if e != nil {
			t.Fatal(e)
		}
		defer next.Close()
		v, _ = s.Snapshot()
		if v.Pair.Phase != relayproto.PairSpent {
			t.Fatal(v.Pair.Phase)
		}
		if e := next.PairBegin(); e != nil {
			t.Fatal(e)
		}
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		v, _ = s.Snapshot()
		if v.Pair.Phase != relayproto.PairSpent {
			t.Fatal("expiry not durably spent", v.Pair.Phase)
		}
	})
}
func TestCapabilitiesAndNotificationSerializationRevokedOffline(t *testing.T) {
	m, s := managerForTest(t)
	if e := m.Connect(ConnectOptions{Origin: "https://relay.example"}); e != nil {
		t.Fatal(e)
	}
	id, _ := relayproto.GenerateIdentity()
	ui, _ := relayproto.Fingerprint("ui", id.PublicKey())
	d := Device{UIID: ui, PublicKey: id.PublicKey(), PairEpoch: bytes.Repeat([]byte{1}, 16), NotificationRoot: bytes.Repeat([]byte{2}, 32), AID: bytes.Repeat([]byte{3}, 16), Name: "phone", AcceptedAt: time.Now().UTC(), State: Active, Generation: 1}
	if e := s.Update(func(v *State) error { v.NextGeneration = 1; v.Devices = append(v.Devices, d); return nil }); e != nil {
		t.Fatal(e)
	}
	cap, e := m.capability(ui, d.PairEpoch, context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e := m.Validate(Capability{}); e == nil {
		t.Fatal("forged capability")
	}
	if e := m.Validate(cap); e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	finished := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- m.WithDevice(context.Background(), ui, func(ctx context.Context, d Device) error { close(entered); <-ctx.Done(); close(finished); return nil })
	}()
	<-entered
	if e := m.Unregister(ui); e != nil {
		t.Fatal(e)
	}
	select {
	case <-finished:
	default:
		t.Fatal("unregister returned before callback exit")
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := m.Validate(cap); e == nil {
		t.Fatal("revoked capability")
	}
	if e := m.WithDevice(context.Background(), ui, func(context.Context, Device) error { t.Fatal("revoked device admitted"); return nil }); e == nil {
		t.Fatal("revoked poll admitted")
	}
	v, _ := s.Snapshot()
	if len(v.Devices) != 0 {
		t.Fatal("revocation not persisted")
	}
}

func TestDeviceReceiptMetadataPersistsExactlyAndUpdatesRecheckRevocation(t *testing.T) {
	m, s := managerForTest(t)
	if e := m.Connect(ConnectOptions{Origin: "https://relay.example"}); e != nil {
		t.Fatal(e)
	}
	id, _ := relayproto.GenerateIdentity()
	ui, _ := relayproto.Fingerprint("ui", id.PublicKey())
	receipt := relayproto.Receipt{Batch: bytes.Repeat([]byte{1}, 16), Through: 17, Token: bytes.Repeat([]byte{2}, 32)}
	d := Device{UIID: ui, PublicKey: id.PublicKey(), PairEpoch: bytes.Repeat([]byte{3}, 16), NotificationRoot: bytes.Repeat([]byte{4}, 32), AID: bytes.Repeat([]byte{5}, 16), AcceptedAt: time.Now().UTC(), State: Active, Generation: 1}
	if e := s.Update(func(v *State) error { v.NextGeneration = 1; v.Devices = append(v.Devices, d); return nil }); e != nil {
		t.Fatal(e)
	}
	if e := m.UpdateDevice(d, func(v *Device) error {
		v.Cursor = receipt.Through
		v.LastAck = &receipt
		v.Pending = &PendingReceipt{Receipt: relayproto.Receipt{Batch: bytes.Repeat([]byte{6}, 16), Through: 21, Token: bytes.Repeat([]byte{7}, 32)}, IDs: []relayproto.Counter{19, 21}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	reloaded, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	v, _ := reloaded.Snapshot()
	saved := v.Devices[0]
	if saved.Cursor != 17 || !saved.LastAck.Equal(receipt) || len(saved.Pending.IDs) != 2 || saved.Pending.IDs[1] != 21 {
		t.Fatal("receipt state lost")
	}
	if e := m.Unregister(ui); e != nil {
		t.Fatal(e)
	}
	if e := m.UpdateDevice(d, func(*Device) error { return nil }); e == nil {
		t.Fatal("revoked generation saved state")
	}
}
func TestOutOfOrderLocalAcceptanceSpendsArmedSlot(t *testing.T) {
	m, s := managerForTest(t)
	if e := m.Connect(ConnectOptions{Origin: "https://relay.example"}); e != nil {
		t.Fatal(e)
	}
	if e := m.PairBegin(); e != nil {
		t.Fatal(e)
	}
	if _, e := m.PairAccept(nil, "123456", nil, "phone"); e == nil {
		t.Fatal("accepted without peer")
	}
	v, _ := s.Snapshot()
	if v.Pair.Phase != relayproto.PairSpent {
		t.Fatal("out-of-order local accept left slot armed")
	}
}
