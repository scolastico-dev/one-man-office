package relayhost

import (
	"context"
	"slices"
)

// Capability can be copied but cannot be minted outside the manager. Its
// authority is checked against live durable device state and cancellation.
type Capability struct {
	manager    *Manager
	ui         string
	epoch      []byte
	generation uint64
	ctx        context.Context
}

func (c Capability) UIID() string       { return c.ui }
func (c Capability) PairEpoch() []byte  { return slices.Clone(c.epoch) }
func (c Capability) Generation() uint64 { return c.generation }
func (c Capability) Context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}
func (m *Manager) capability(ui string, epoch []byte, ctx context.Context) (Capability, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, e := m.findDeviceLocked(ui, epoch, true)
	if e != nil || !m.enabled {
		return Capability{}, ErrDenied
	}
	return Capability{manager: m, ui: ui, epoch: slices.Clone(d.PairEpoch), generation: d.Generation, ctx: ctx}, nil
}
func (m *Manager) Validate(c Capability) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.manager != m || c.ctx == nil || c.ctx.Err() != nil || !m.enabled {
		return ErrDenied
	}
	d, e := m.findDeviceLocked(c.ui, c.epoch, true)
	if e != nil {
		return e
	}
	if d.Generation != c.generation {
		return ErrDenied
	}
	return nil
}

// Devices returns private isolated snapshots. Callers must project public
// fields explicitly before displaying or encoding them.
func (m *Manager) Devices() ([]Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, e := m.store.Snapshot()
	return v.Devices, e
}
func (m *Manager) LiveConnections(ui string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	if m.current != nil {
		for _, p := range m.current.peers {
			if p.ui == ui && p.ctx.Err() == nil {
				n++
			}
		}
	}
	return n
}

func (m *Manager) Unregister(ui string) error {
	m.admin.Lock()
	defer m.admin.Unlock()
	m.mu.Lock()
	e := m.store.Update(func(v *State) error {
		for i, d := range v.Devices {
			if d.UIID == ui {
				v.Devices = slices.Delete(v.Devices, i, i+1)
				return nil
			}
		}
		return ErrDenied
	})
	if e != nil {
		m.mu.Unlock()
		return e
	}
	var polls []*notificationWork
	var peers []*peer
	if m.current != nil {
		for _, q := range m.current.polls {
			if q.ui == ui {
				q.cancel()
				polls = append(polls, q)
			}
		}
		for _, p := range m.current.peers {
			if p.ui == ui {
				p.cancel()
				peers = append(peers, p)
			}
		}
	}
	jobs := m.cancelJobsLocked(ui)
	m.mu.Unlock()
	for _, p := range peers {
		<-p.done
	}
	waitJobs(jobs)
	for _, q := range polls {
		<-q.done
	}
	m.mu.Lock()
	delete(m.deviceLocks, ui)
	m.mu.Unlock()
	return nil
}

type deviceWork struct {
	ui     string
	cancel context.CancelFunc
	done   chan struct{}
}

func (m *Manager) cancelJobsLocked(ui string) []*deviceWork {
	var out []*deviceWork
	for w := range m.jobs {
		if ui == "" || w.ui == ui {
			w.cancel()
			out = append(out, w)
		}
	}
	return out
}
func waitJobs(jobs []*deviceWork) {
	for _, w := range jobs {
		<-w.done
	}
}

// WithDevice serializes notification operations per active device and joins
// cancellation on unregister/disconnect/close. It never holds the global store
// lock over f. Use UpdateDevice to commit cursor/last_ack/pending after queries.
func (m *Manager) WithDevice(ctx context.Context, ui string, f func(context.Context, Device) error) error {
	m.mu.Lock()
	d, e := m.findDeviceLocked(ui, nil, true)
	if e != nil || !m.enabled {
		m.mu.Unlock()
		return ErrDenied
	}
	workCtx, cancel := context.WithCancel(m.ctx)
	stop := context.AfterFunc(ctx, cancel)
	w := &deviceWork{ui: ui, cancel: cancel, done: make(chan struct{})}
	m.jobs[w] = true
	gate := m.deviceLocks[ui]
	if gate == nil {
		gate = make(chan struct{}, 1)
		gate <- struct{}{}
		m.deviceLocks[ui] = gate
	}
	m.mu.Unlock()
	defer func() {
		stop()
		cancel()
		m.mu.Lock()
		delete(m.jobs, w)
		close(w.done)
		m.mu.Unlock()
	}()
	select {
	case <-workCtx.Done():
		return workCtx.Err()
	case <-gate:
	}
	defer func() { gate <- struct{}{} }()
	m.mu.Lock()
	latest, e := m.findDeviceLocked(ui, d.PairEpoch, true)
	if e == nil && latest.Generation != d.Generation {
		e = ErrDenied
	}
	m.mu.Unlock()
	if e != nil {
		return e
	}
	if workCtx.Err() != nil {
		return workCtx.Err()
	}
	return f(workCtx, latest)
}

// UpdateDevice saves notification state only for the exact active generation.
// f must not do network or board work. Identity/authority fields are immutable.
func (m *Manager) UpdateDevice(d Device, f func(*Device) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled || m.ctx.Err() != nil || m.closed {
		return ErrDenied
	}
	return m.store.Update(func(v *State) error {
		for i, current := range v.Devices {
			if current.UIID == d.UIID && current.State == Active && current.Generation == d.Generation && slices.Equal(current.PairEpoch, d.PairEpoch) {
				next := cloneDevice(current)
				if e := f(&next); e != nil {
					return e
				}
				current.Cursor = next.Cursor
				current.LastAck = cloneReceipt(next.LastAck)
				current.Pending = next.Pending
				v.Devices[i] = current
				return nil
			}
		}
		return ErrDenied
	})
}
