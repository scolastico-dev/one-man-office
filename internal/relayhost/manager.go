package relayhost

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// Callbacks are deliberately narrow integration points. A missing callback
// grants no application or notification access. Callbacks must join their work
// before returning when their context is cancelled.
type Callbacks struct {
	ServeSession      func(context.Context, Capability, relayproto.RecordIO) error
	PollNotifications func(context.Context, Device, relayproto.Poll) (relayproto.NotificationEnvelope, error)
}

// Options holds startup configuration and durable local configuration updates.
type Options struct {
	Origin     string
	Enabled    bool
	SaveConfig func(origin string, enabled bool) error
	Callbacks  Callbacks
	// Dial is an optional transport seam; nil uses certificate-verified WebSocket dialing.
	Dial func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
}
type ConnectOptions struct {
	Origin   string
	ClientID string
	NewID    bool
	Password *string
}
type Status struct {
	State        string `json:"state"`
	Enabled      bool   `json:"enabled"`
	Origin       string `json:"origin"`
	HostID       string `json:"host_id"`
	HostFP       string `json:"host_fp"`
	RetrySeconds int    `json:"retry_seconds"`
}

type Manager struct {
	admin       sync.Mutex
	mu          sync.Mutex
	store       *Store
	opts        Options
	ctx         context.Context
	cancel      context.CancelFunc
	enabled     bool
	state       string
	retry       int
	pair        *relayproto.PairHost
	identity    *relayproto.Identity
	expiry      *time.Timer
	current     *connection
	wake        chan struct{}
	runDone     chan struct{}
	running     bool
	closed      bool
	jobs        map[*deviceWork]bool
	deviceLocks map[string]chan struct{}
	callbacks   Callbacks
}

func NewManager(store *Store, o Options) (*Manager, error) {
	if store == nil {
		return nil, ErrState
	}
	v, e := store.Snapshot()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{store: store, opts: o, ctx: ctx, cancel: cancel, state: "disabled", wake: make(chan struct{}, 1), runDone: make(chan struct{}), jobs: map[*deviceWork]bool{}, deviceLocks: map[string]chan struct{}{}, callbacks: o.Callbacks}
	store.mu.Lock()
	store.onFailure = cancel
	store.mu.Unlock()
	if len(v.Seed) > 0 {
		if e = m.restorePair(v); e != nil {
			cancel()
			return nil, e
		}
	}
	if o.Enabled {
		r, e := relayproto.NewRoute(o.Origin, v.HostID, true)
		switch {
		case len(v.Seed) == 0:
			m.state = "missing_state"
		case e != nil || r.Origin != v.Origin:
			m.state = "configuration_mismatch"
		default:
			m.enabled = true
			m.state = "retrying"
		}
	}
	return m, nil
}
func (m *Manager) restorePair(v State) error {
	id, e := relayproto.ImportIdentity(v.Seed)
	if e != nil {
		return ErrState
	}
	p, e := relayproto.NewPairHost(relayproto.Route{Origin: v.Origin, HostID: v.HostID}, id, pairStore{m.store}, time.Now, nil)
	if e != nil {
		return e
	}
	if e = p.Recover(); e != nil {
		return e
	}
	m.identity = id
	m.pair = p
	return nil
}
func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) SetCallbacks(c Callbacks) { m.mu.Lock(); m.callbacks = c; m.mu.Unlock() }
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, e := m.store.Snapshot()
	state := m.state
	if e != nil {
		state = "state_error"
	}
	return Status{State: state, Enabled: m.enabled && m.ctx.Err() == nil, Origin: v.Origin, HostID: v.HostID, HostFP: v.HostFP, RetrySeconds: m.retry}
}

// Connect commits identity/route/admission before enabling and local success.
// Replacing the routing name never silently replaces the host key.
func (m *Manager) Connect(o ConnectOptions) error {
	m.admin.Lock()
	defer m.admin.Unlock()
	if o.NewID && o.ClientID != "" {
		return errors.New("choose either client-id or new-id")
	}
	v, e := m.store.Snapshot()
	if e != nil {
		return e
	}
	id := o.ClientID
	if id == "" && !o.NewID {
		id = v.HostID
	}
	if id == "" {
		b := make([]byte, 16)
		if _, e = rand.Read(b); e != nil {
			return e
		}
		id = base64.RawURLEncoding.EncodeToString(b)
	}
	r, e := relayproto.NewRoute(o.Origin, id, true)
	if e != nil {
		return errors.New("relay requires an HTTPS origin and a valid client id (HTTP only for loopback development)")
	}
	if o.Password != nil && len(*o.Password) > 4096 {
		return errors.New("relay password exceeds 4096 bytes")
	}
	if e = m.pause(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return ErrState
	}
	e = m.store.Update(func(v *State) error {
		if len(v.Seed) == 0 {
			key, e := relayproto.GenerateIdentity()
			if e != nil {
				return e
			}
			v.Seed = key.Seed()
			v.HostFP, _ = relayproto.Fingerprint("host", key.PublicKey())
		}
		if v.Origin != r.Origin {
			v.AdmissionSecret = ""
		}
		v.Origin = r.Origin
		v.HostID = id
		if o.Password != nil {
			v.AdmissionSecret = *o.Password
		}
		v.Pair = relayproto.PairSlot{Phase: relayproto.PairClosed}
		return nil
	})
	if e != nil {
		return e
	}
	v, e = m.store.Snapshot()
	if e != nil {
		return e
	}
	if e = m.restorePair(v); e != nil {
		return e
	}
	if m.opts.SaveConfig != nil {
		if e = m.opts.SaveConfig(r.Origin, true); e != nil {
			m.state = "configuration_error"
			return e
		}
	}
	m.opts.Origin = r.Origin
	m.opts.Enabled = true
	m.enabled = true
	m.state = "retrying"
	m.signal()
	return nil
}

// pause closes admission first, then joins every live callback outside locks.
func (m *Manager) pause() error {
	m.mu.Lock()
	m.enabled = false
	m.state = "disabled"
	m.retry = 0
	c := m.current
	if c != nil {
		c.cancel()
	}
	jobs := m.cancelJobsLocked("")
	if m.expiry != nil {
		m.expiry.Stop()
		m.expiry = nil
	}
	m.signal()
	m.mu.Unlock()
	if c != nil {
		<-c.done
	}
	waitJobs(jobs)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pair != nil {
		return m.pair.Abort()
	}
	return nil
}
func (m *Manager) Disconnect() error {
	m.admin.Lock()
	defer m.admin.Unlock()
	if e := m.pause(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, e := m.store.Snapshot()
	if e != nil {
		return e
	}
	if m.opts.SaveConfig != nil {
		if e = m.opts.SaveConfig(v.Origin, false); e != nil {
			return e
		}
	}
	m.opts.Enabled = false
	return nil
}
func (m *Manager) Close() error {
	m.admin.Lock()
	defer m.admin.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.enabled = false
	m.state = "disabled"
	m.cancel()
	if m.expiry != nil {
		m.expiry.Stop()
	}
	c := m.current
	if c != nil {
		c.cancel()
	}
	jobs := m.cancelJobsLocked("")
	running := m.running
	m.mu.Unlock()
	if c != nil {
		<-c.done
	}
	waitJobs(jobs)
	if running {
		<-m.runDone
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pair != nil {
		return m.pair.Abort()
	}
	return nil
}

func (m *Manager) findDeviceLocked(ui string, epoch []byte, active bool) (Device, error) {
	if m.closed || m.ctx.Err() != nil {
		return Device{}, ErrDenied
	}
	v, e := m.store.Snapshot()
	if e != nil {
		return Device{}, e
	}
	for _, d := range v.Devices {
		if d.UIID == ui && (len(epoch) == 0 || slices.Equal(epoch, d.PairEpoch)) && (!active || d.State == Active) {
			return d, nil
		}
	}
	return Device{}, ErrDenied
}
