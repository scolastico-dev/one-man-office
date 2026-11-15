package server

import (
	"sync"
	"sync/atomic"
)

// refsLock keeps ticket writes and settings removals apart. The board holds
// it (board.Options.RefsLock) from checking a new ticket's projects and model
// until the ticket is committed; the routes that remove a project or drop a
// model hold it from asking the board which open tickets use them until the
// settings store has written the removal. Whichever comes second sees the
// first: a ticket write fails as for any unknown project or model, a removal
// is refused with in_use.
//
// Lock order: refsLock, then the board's database (one connection, so a
// transaction is a lock too), then the settings store's locks. The board
// takes refsLock before it begins a transaction or asks the settings; the
// removal routes take it before they read the board or write the settings;
// neither the store nor the board takes it while holding its own locks.
type refsLock struct {
	mu sync.Mutex

	// Test seams. waiting runs when Lock finds the lock taken, before it
	// waits; looked runs in the board's environment after a project, model
	// or branch lookup answered (kind "project", "model" or "branch", and
	// the id or path), with the lock held; removing runs in a removal route
	// after its in-use check, before the settings write, with the lock held.
	waiting  atomic.Pointer[func()]
	looked   atomic.Pointer[func(kind, id string)]
	removing atomic.Pointer[func()]
}

func (l *refsLock) Lock() {
	if l.mu.TryLock() {
		return
	}
	if f := l.waiting.Load(); f != nil {
		(*f)()
	}
	l.mu.Lock()
}

func (l *refsLock) Unlock() { l.mu.Unlock() }

// lookedUp calls the looked seam.
func (l *refsLock) lookedUp(kind, id string) {
	if l == nil {
		return
	}
	if f := l.looked.Load(); f != nil {
		(*f)(kind, id)
	}
}

// removal runs fn, a settings removal with its in-use check, under the lock.
// check runs first; fn runs only when check passed.
func (s *Server) removal(check func() error, fn func() error) error {
	s.refs.Lock()
	defer s.refs.Unlock()
	if err := check(); err != nil {
		return err
	}
	if f := s.refs.removing.Load(); f != nil {
		(*f)()
	}
	return fn()
}
