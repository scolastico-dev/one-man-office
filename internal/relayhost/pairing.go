package relayhost

import (
	"bytes"
	"encoding/base64"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

type PendingPair struct {
	Phase   relayproto.PairPhase `json:"phase"`
	Expires time.Time            `json:"expires"`
	AID     string               `json:"aid"`
	UIID    string               `json:"ui_id"`
}

func (m *Manager) PairBegin() error {
	m.admin.Lock()
	defer m.admin.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pair == nil || m.closed || !m.enabled {
		return ErrPair
	}
	// A new locally authorized ceremony cancels the previous endpoint first.
	m.abortPairPeerLocked()
	if e := m.pair.Begin(); e != nil {
		return e
	}
	v, e := m.store.Snapshot()
	if e != nil {
		return e
	}
	if m.expiry != nil {
		m.expiry.Stop()
	}
	window := bytes.Clone(v.Pair.Transcript.Window)
	m.expiry = time.AfterFunc(time.Until(v.Pair.Expires), func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.ctx.Err() != nil {
			return
		}
		v, e := m.store.Snapshot()
		if e == nil && bytes.Equal(v.Pair.Transcript.Window, window) && !time.Now().Before(v.Pair.Expires) && v.Pair.Phase != relayproto.PairAccepted {
			m.abortPairPeerLocked()
			_ = m.pair.Abort()
		}
	})
	return nil
}
func (m *Manager) abortPairPeerLocked() {
	if m.current == nil {
		return
	}
	v, e := m.store.Snapshot()
	if e != nil {
		return
	}
	if p := m.current.peers[v.Pair.Connection]; p != nil && p.pairing && v.Pair.Phase != relayproto.PairAccepted {
		p.cancel()
	}
}
func (m *Manager) PairCancel() error {
	m.admin.Lock()
	defer m.admin.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pair == nil {
		return ErrPair
	}
	m.abortPairPeerLocked()
	if m.expiry != nil {
		m.expiry.Stop()
	}
	return m.pair.Abort()
}
func (m *Manager) PairPending() (PendingPair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, e := m.store.Snapshot()
	if e != nil {
		return PendingPair{}, e
	}
	if m.pair != nil && !v.Pair.Expires.IsZero() && !time.Now().Before(v.Pair.Expires) && v.Pair.Phase != relayproto.PairAccepted && v.Pair.Phase != relayproto.PairSpent {
		m.abortPairPeerLocked()
		if e = m.pair.Abort(); e != nil {
			return PendingPair{}, e
		}
		v, e = m.store.Snapshot()
		if e != nil {
			return PendingPair{}, e
		}
	}
	ui := ""
	if len(v.Pair.Transcript.PKU) > 0 {
		ui, _ = relayproto.Fingerprint("ui", v.Pair.Transcript.PKU)
	}
	return PendingPair{Phase: v.Pair.Phase, Expires: v.Pair.Expires, AID: base64.RawURLEncoding.EncodeToString(v.Pair.Transcript.AID), UIID: ui}, nil
}
func (m *Manager) PairAccept(aid []byte, pin string, pk []byte, name string) (Device, error) {
	m.admin.Lock()
	defer m.admin.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pair == nil {
		return Device{}, ErrPair
	}
	if m.current == nil || !m.enabled {
		if e := m.pair.Abort(); e != nil {
			return Device{}, e
		}
		return Device{}, ErrPair
	}
	v, e := m.store.Snapshot()
	if e != nil {
		return Device{}, e
	}
	p := m.current.peers[v.Pair.Connection]
	if p == nil || p.ctx.Err() != nil {
		_ = m.pair.Abort()
		return Device{}, ErrPair
	}
	if len(name) > 128 || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		_ = m.pair.Abort()
		p.cancel()
		return Device{}, ErrPair
	}
	action, e := m.pair.Accept(p.key, aid, pin, pk)
	if e != nil {
		p.cancel()
		return Device{}, e
	}
	a := action.Acceptance
	ui, _ := relayproto.Fingerprint("ui", a.Transcript.PKU)
	// Do not replace existing trust. Re-pairing requires unregister.
	d := Device{UIID: ui, PublicKey: bytes.Clone(a.Transcript.PKU), PairEpoch: bytes.Clone(a.Transcript.PairEpoch), NotificationRoot: bytes.Clone(a.NotificationRoot), AID: bytes.Clone(a.Transcript.AID), Name: name, AcceptedAt: time.Now().UTC(), State: Active}
	e = m.store.Update(func(v *State) error {
		for _, old := range v.Devices {
			if old.UIID == ui {
				return ErrDenied
			}
		}
		if v.NextGeneration == ^uint64(0) {
			return ErrState
		}
		v.NextGeneration++
		d.Generation = v.NextGeneration
		v.Devices = append(v.Devices, d)
		return nil
	})
	if e != nil {
		p.cancel()
		return Device{}, e
	}
	p.ui = ui
	p.epoch = bytes.Clone(d.PairEpoch)
	p.generation = d.Generation
	if m.expiry != nil {
		m.expiry.Stop()
	}
	// The queue notification follows the durable acceptance. Losing this reply
	// preserves the active device for exact reconnect recovery.
	select {
	case p.accepted <- cloneDevice(d):
	default:
		p.cancel()
	}
	return cloneDevice(d), nil
}
