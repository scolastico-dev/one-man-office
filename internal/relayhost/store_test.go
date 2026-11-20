package relayhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

func TestStoreIdentityTransactionsAndRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "relay")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Seed) != 0 {
		t.Fatal("opening unused store generated identity")
	}
	id, _ := relayproto.GenerateIdentity()
	if err := s.Update(func(v *State) error {
		v.Seed = id.Seed()
		v.HostFP, _ = relayproto.Fingerprint("host", id.PublicKey())
		v.Origin = "https://relay.example"
		v.HostID = "office"
		v.AdmissionSecret = "private"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Snapshot()
	v.Seed[0] ^= 255
	sentinel := errors.New("abort")
	if err := s.Update(func(v *State) error { v.HostID = "wrong"; v.Seed[0] ^= 255; return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	again, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := again.Snapshot()
	if saved.HostID != "office" || !bytes.Equal(saved.Seed, id.Seed()) {
		t.Fatal("transaction leaked or identity replaced")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(dir, "state.json"))
		if info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
		info, _ = os.Stat(dir)
		if info.Mode().Perm() != 0700 {
			t.Fatal(info.Mode())
		}
	}
}

func TestStoreFailedSaveLatchesClosed(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "relay"))
	if err != nil {
		t.Fatal(err)
	}
	s.write = func(string, []byte, os.FileMode) error { return errors.New("disk failed") }
	if err := s.Update(func(v *State) error { v.NextGeneration = 1; return nil }); err == nil {
		t.Fatal("save succeeded")
	}
	if _, err := s.Snapshot(); err == nil {
		t.Fatal("read after failed save")
	}
	if err := s.Update(func(*State) error { return nil }); err == nil {
		t.Fatal("update after failed save")
	}
}

func TestStoreRejectsCorruption(t *testing.T) {
	for _, raw := range []string{`{}`, `{"version":2}`, `{"version":1,"version":1}`, `{"version":1,"devices":[{"state":"mystery"}]}`, `null`, `{"version":1} {}`} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenStore(dir); err == nil {
				t.Fatal("accepted corrupt state")
			}
		})
	}
}

func TestStoreRejectsLinkedDirectoryAndState(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(link); err == nil {
		t.Fatal("followed directory link")
	}
	if err := os.WriteFile(filepath.Join(base, "secret"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "secret"), filepath.Join(target, "state.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(target); err == nil {
		t.Fatal("followed state link")
	}
}

func TestStoreRejectsCorruptTrustAndReceipts(t *testing.T) {
	m, s := managerForTest(t)
	if e := m.Connect(ConnectOptions{Origin: "https://relay.example"}); e != nil {
		t.Fatal(e)
	}
	id, _ := relayproto.GenerateIdentity()
	ui, _ := relayproto.Fingerprint("ui", id.PublicKey())
	v, _ := s.Snapshot()
	v.NextGeneration = 1
	v.Devices = []Device{{UIID: ui, PublicKey: id.PublicKey(), PairEpoch: bytes.Repeat([]byte{1}, 16), NotificationRoot: bytes.Repeat([]byte{2}, 32), AID: bytes.Repeat([]byte{3}, 16), AcceptedAt: time.Now().UTC(), State: Active, Generation: 1}}
	for name, corrupt := range map[string]func(*State){
		"duplicate-device":         func(v *State) { v.Devices = append(v.Devices, v.Devices[0]) },
		"unknown-device-state":     func(v *State) { v.Devices[0].State = "awaiting-confirmation" },
		"invalid-key":              func(v *State) { v.Devices[0].PublicKey = v.Devices[0].PublicKey[:100] },
		"changed-host-fingerprint": func(v *State) { v.HostFP = "bad" },
		"invalid-last-ack": func(v *State) {
			v.Devices[0].Cursor = 4
			v.Devices[0].LastAck = &relayproto.Receipt{Batch: bytes.Repeat([]byte{4}, 16), Through: 3, Token: bytes.Repeat([]byte{5}, 32)}
		},
		"unordered-pending": func(v *State) {
			v.Devices[0].Pending = &PendingReceipt{Receipt: relayproto.Receipt{Batch: bytes.Repeat([]byte{4}, 16), Through: 3, Token: bytes.Repeat([]byte{5}, 32)}, IDs: []relayproto.Counter{4, 3}}
		},
		"newer-version": func(v *State) { v.Version = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := cloneState(v)
			corrupt(&bad)
			raw, e := json.Marshal(bad)
			if e != nil {
				t.Fatal(e)
			}
			dir := t.TempDir()
			if e = os.WriteFile(filepath.Join(dir, "state.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = OpenStore(dir); e == nil {
				t.Fatal("accepted corrupt trust")
			}
		})
	}
}
