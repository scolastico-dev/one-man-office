package supervisor

import (
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

const (
	wakeSubject = "Office unfrozen"
	wakeBody    = "The office is unfrozen. Read your inbox and resume your assigned work."
)

// BeginFreeze blocks every new process spawn. The tools plugin separately
// broadcasts the role-specific halt instructions so orchestration remains in
// the single Lua action exposed to users.
func (s *Supervisor) BeginFreeze(actor string) error {
	// Wait for a process already crossing the final spawn boundary to register.
	// The following Lua broadcast will include it, and no process can start
	// after this method acknowledges the freeze.
	s.spawnGate.Lock()
	defer s.spawnGate.Unlock()
	s.mu.Lock()
	if s.frozen {
		s.mu.Unlock()
		return nil
	}
	s.frozen = true
	s.mu.Unlock()

	db.AppendEvent(s.DB, "office_frozen", actor, 0, "all agent spawning halted")
	return nil
}

// EndFreezeByCEO durably broadcasts the wake-up before atomically reopening
// every spawn path. The write gate prevents ordinary resume requests from
// racing between the frozen check and state transition.
func (s *Supervisor) EndFreezeByCEO(actor string) error {
	s.spawnGate.Lock()
	defer s.spawnGate.Unlock()
	s.mu.Lock()
	frozen := s.frozen
	s.mu.Unlock()
	if !frozen {
		return fmt.Errorf("office is not frozen")
	}
	if _, err := s.Mail.Send(actor, "", wakeSubject, wakeBody, bus.PrioUrgent); err != nil {
		return fmt.Errorf("send global wake-up mail: %w", err)
	}
	s.mu.Lock()
	s.frozen = false
	s.safeMode = false
	s.ceoSpawnHalted = false
	waiters := make([]chan struct{}, 0, len(s.waiters))
	for _, waiter := range s.waiters {
		waiters = append(waiters, waiter)
	}
	s.mu.Unlock()
	for _, waiter := range waiters {
		select {
		case waiter <- struct{}{}:
		default:
		}
	}
	db.AppendEvent(s.DB, "office_unfrozen", actor, 0, "global wake-up mail sent; full office spawning resumed")
	s.kickDispatch()
	go s.resumePendingReviews()
	return nil
}
