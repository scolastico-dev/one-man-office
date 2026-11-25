// Package relayhost owns the outbound relay connection and durable endpoint trust.
// It does not import the board or HTTP server.
package relayhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

var (
	ErrState  = errors.New("relay private state unavailable; repair it before restarting")
	ErrDenied = errors.New("relay device is not authorized")
	ErrPair   = errors.New("relay pairing attempt is unavailable or spent; begin a new local ceremony")
)

const (
	Active = "active"
)

// PendingReceipt contains receipt metadata only; notification content stays in the board.
type PendingReceipt struct {
	Receipt relayproto.Receipt   `json:"receipt"`
	IDs     []relayproto.Counter `json:"ids"`
}

// Device is an isolated snapshot. NotificationRoot is private and must never be
// serialized into administration output. Generation changes on each pairing.
type Device struct {
	UIID             string              `json:"ui_id"`
	PublicKey        relayproto.Bytes    `json:"public_key"`
	PairEpoch        relayproto.Bytes    `json:"pair_epoch"`
	NotificationRoot relayproto.Bytes    `json:"notification_root"`
	AID              relayproto.Bytes    `json:"aid"`
	Name             string              `json:"name"`
	AcceptedAt       time.Time           `json:"accepted_at"`
	State            string              `json:"state"`
	Generation       uint64              `json:"generation"`
	Cursor           relayproto.Counter  `json:"cursor"`
	LastAck          *relayproto.Receipt `json:"last_ack"`
	Pending          *PendingReceipt     `json:"pending"`
}

// State version 1 is private, host-global, and atomically replaced on updates.
type State struct {
	Version         int                 `json:"version"`
	Seed            relayproto.Bytes    `json:"seed"`
	HostFP          string              `json:"host_fp"`
	Origin          string              `json:"origin"`
	HostID          string              `json:"host_id"`
	AdmissionSecret string              `json:"admission_secret"`
	Pair            relayproto.PairSlot `json:"pair"`
	Devices         []Device            `json:"devices"`
	NextGeneration  uint64              `json:"next_generation"`
}

// Store owns all state transactions. A failed disk write latches it closed;
// callback validation errors leave the previous state intact.
type Store struct {
	mu        sync.Mutex
	dir       string
	state     State
	failed    bool
	write     func(string, []byte, os.FileMode) error
	onFailure func()
}

// OpenStore does not generate an identity. Missing state is usable only for an
// explicit local Connect, never for silently enabling an imported config.
func OpenStore(dir string) (*Store, error) {
	if err := globalhome.EnsurePrivateDir(dir); err != nil {
		return nil, ErrState
	}
	s := &Store{dir: dir, write: globalhome.WriteFileAtomic, state: State{Version: 1, Pair: relayproto.PairSlot{Phase: relayproto.PairClosed}, Devices: []Device{}}}
	raw, err := homefile.Read(filepath.Join(dir, "state.json"), homefile.MaxRelayState)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, ErrState
	}
	if err = strictState(raw, &s.state); err != nil {
		return nil, ErrState
	}
	if err = s.state.validate(); err != nil {
		return nil, ErrState
	}
	return s, nil
}

func cloneState(v State) State {
	v.Seed = slices.Clone(v.Seed)
	v.Pair = v.Pair.Clone()
	v.Devices = slices.Clone(v.Devices)
	for i := range v.Devices {
		v.Devices[i] = cloneDevice(v.Devices[i])
	}
	return v
}
func cloneReceipt(r *relayproto.Receipt) *relayproto.Receipt {
	if r == nil {
		return nil
	}
	v := *r
	v.Batch = slices.Clone(v.Batch)
	v.Token = slices.Clone(v.Token)
	return &v
}
func cloneDevice(v Device) Device {
	v.PublicKey = slices.Clone(v.PublicKey)
	v.PairEpoch = slices.Clone(v.PairEpoch)
	v.NotificationRoot = slices.Clone(v.NotificationRoot)
	v.AID = slices.Clone(v.AID)
	v.LastAck = cloneReceipt(v.LastAck)
	if v.Pending != nil {
		v.Pending = &PendingReceipt{Receipt: *cloneReceipt(&v.Pending.Receipt), IDs: slices.Clone(v.Pending.IDs)}
	}
	return v
}

func (s *Store) Snapshot() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return State{}, ErrState
	}
	return cloneState(s.state), nil
}
func (s *Store) Update(f func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return ErrState
	}
	v := cloneState(s.state)
	if err := f(&v); err != nil {
		return err
	}
	if err := v.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > homefile.MaxRelayState {
		return ErrState
	}
	if err = globalhome.EnsurePrivateDir(s.dir); err == nil {
		err = s.write(filepath.Join(s.dir, "state.json"), append(raw, '\n'), 0600)
	}
	if err != nil {
		s.failed = true
		if s.onFailure != nil {
			s.onFailure()
		}
		return ErrState
	}
	s.state = cloneState(v)
	return nil
}
func (v State) validate() error {
	if v.Version != 1 || v.Devices == nil || len(v.Devices) > 512 {
		return ErrState
	}
	if len(v.Seed) == 0 {
		if v.HostFP != "" || v.Origin != "" || v.HostID != "" || v.AdmissionSecret != "" || len(v.Devices) > 0 || v.Pair.Phase != relayproto.PairClosed {
			return ErrState
		}
	} else {
		id, e := relayproto.ImportIdentity(v.Seed)
		if e != nil {
			return ErrState
		}
		fp, _ := relayproto.Fingerprint("host", id.PublicKey())
		if fp != v.HostFP {
			return ErrState
		}
		r, e := relayproto.NewRoute(v.Origin, v.HostID, true)
		if e != nil || r.Origin != v.Origin {
			return ErrState
		}
	}
	switch v.Pair.Phase {
	case relayproto.PairClosed, relayproto.PairSpent:
	case relayproto.PairArmed, relayproto.PairCommitted, relayproto.PairRevealed, relayproto.PairProved, relayproto.PairAccepted:
		if v.Pair.Expires.IsZero() || v.Pair.Transcript.Route != (relayproto.Route{Origin: v.Origin, HostID: v.HostID}) {
			return ErrState
		}
		id, e := relayproto.ImportIdentity(v.Seed)
		if e != nil || !bytes.Equal(v.Pair.Transcript.PKH, id.PublicKey()) || len(v.Pair.Transcript.Window) != 16 {
			return ErrState
		}
		if v.Pair.Phase != relayproto.PairArmed {
			if v.Pair.Connection == "" || len(v.Pair.Transcript.AID) != 16 || len(v.Pair.Transcript.C) != 48 || len(v.Pair.Transcript.RH) != 32 || len(v.Pair.Transcript.PairEpoch) != 16 {
				return ErrState
			}
		}
		if v.Pair.Phase == relayproto.PairRevealed || v.Pair.Phase == relayproto.PairProved || v.Pair.Phase == relayproto.PairAccepted {
			if _, e := v.Pair.Transcript.Encode(); e != nil {
				return ErrState
			}
		}
		if (v.Pair.Phase == relayproto.PairProved || v.Pair.Phase == relayproto.PairAccepted) && len(v.Pair.NotificationRoot) != 32 {
			return ErrState
		}
	default:
		return ErrState
	}
	seen := map[string]bool{}
	for _, d := range v.Devices {
		fp, e := relayproto.Fingerprint("ui", d.PublicKey)
		if e != nil || d.UIID != fp || seen[fp] || len(d.PairEpoch) != 16 || len(d.NotificationRoot) != 32 || len(d.AID) != 16 || d.AcceptedAt.IsZero() || d.Generation == 0 || d.Generation > v.NextGeneration || d.State != Active || len(d.Name) > 128 {
			return ErrState
		}
		seen[fp] = true
		if d.LastAck != nil && (!validReceipt(*d.LastAck) || d.LastAck.Through != d.Cursor) {
			return ErrState
		}
		if d.Pending != nil {
			p := d.Pending
			if !validReceipt(p.Receipt) || len(p.IDs) == 0 || len(p.IDs) > 50 || p.Receipt.Through != p.IDs[len(p.IDs)-1] {
				return ErrState
			}
			last := d.Cursor
			for _, n := range p.IDs {
				if n <= last {
					return ErrState
				}
				last = n
			}
		}
	}
	return nil
}
func validReceipt(r relayproto.Receipt) bool {
	return len(r.Batch) == 16 && len(r.Token) == 32 && r.Through > 0
}

// Strict local-state JSON rejects duplicate, missing, unknown and case-variant
// fields, including nested devices/receipts. Bytes/time use their own decoders.
func strictState(raw []byte, out *State) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := uniqueJSON(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return ErrState
	}
	if err := exactFields(raw, reflect.TypeOf(*out)); err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return ErrState
	}
	tok, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return ErrState
			}
			seen[s] = true
			if e := uniqueJSON(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e := uniqueJSON(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return ErrState
	}
	_, e = d.Token()
	return e
}
func exactFields(raw []byte, t reflect.Type) error {
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(raw, []byte("null")) {
			return nil
		}
		return exactFields(raw, t.Elem())
	}
	if t == reflect.TypeFor[time.Time]() || t == reflect.TypeFor[relayproto.Bytes]() {
		return nil
	}
	if t.Kind() == reflect.Slice {
		var a []json.RawMessage
		if json.Unmarshal(raw, &a) != nil {
			return ErrState
		}
		for _, r := range a {
			if e := exactFields(r, t.Elem()); e != nil {
				return e
			}
		}
		return nil
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || len(obj) != t.NumField() {
		return ErrState
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Tag.Get("json")
		if name == "" {
			name = f.Name
		}
		r, ok := obj[name]
		if !ok {
			return ErrState
		}
		if e := exactFields(r, f.Type); e != nil {
			return e
		}
	}
	return nil
}

type pairStore struct{ store *Store }

func (s pairStore) Update(f func(*relayproto.PairSlot) error) error {
	return s.store.Update(func(v *State) error { return f(&v.Pair) })
}
