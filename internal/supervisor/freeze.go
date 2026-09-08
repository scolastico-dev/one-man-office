package supervisor

import (
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

const (
	freezeSubject = "office frozen"
	freezeBody    = `OFFICE FREEZE REQUESTED.

Halt your current action immediately. Do not start new work, create jobs, or run omo commands in a background process or subshell. Do not call omo done. If your role permits it, call omo wait and remain parked until the user unfreezes the office.

The CEO must halt at its prompt and wait for the user's global wake-up mail. It must not spawn work while the office is frozen.`
	wakeSubject = "office unfrozen"
	wakeBody    = `The user has unfrozen the office. Read your inbox, resume only your assigned role, and run omo commands only in a blocking first-level shell.`
)

// BeginFreeze holds the office open while every agent is told to stop at a
// safe, user-controlled point. It intentionally does not stop the process or
// requeue jobs, so the office can resume in place after connectivity returns.
func (s *Supervisor) BeginFreeze(actor string) error {
	s.mu.Lock()
	if s.frozen {
		s.mu.Unlock()
		return fmt.Errorf("office is already frozen")
	}
	s.frozen = true
	s.firefighterPaused = true
	s.ceoSpawnHalted = true
	s.mu.Unlock()

	db.AppendEvent(s.DB, "office_frozen", actor, 0, "all agent spawning halted")
	_, _ = s.Mail.Send(bus.SystemSender, "", freezeSubject, freezeBody, bus.PrioUrgent)
	agents, _ := db.LivingAgents(s.DB)
	for _, agent := range agents {
		if sess, ok := s.Session(agent.Name); ok {
			name := agent.Name
			go func() {
				if err := sess.SendPrompt(freezeBody); err != nil {
					db.AppendEvent(s.DB, "office_freeze_injection_error", name, 0, err.Error())
					return
				}
				db.AppendEvent(s.DB, "office_freeze_injected", name, 0, "")
			}()
		}
	}
	return nil
}

// EndFreeze sends durable global wake-up mail before reopening the dispatcher.
// Mail notification releases agents parked in omo wait.
func (s *Supervisor) EndFreeze(actor string) error {
	if !s.Frozen() {
		return fmt.Errorf("office is not frozen")
	}
	_, _ = s.Mail.Send(bus.SystemSender, "", wakeSubject, wakeBody, bus.PrioUrgent)
	s.ResumeSpawning(actor)
	db.AppendEvent(s.DB, "office_unfrozen", actor, 0, "global wake-up mail sent")
	return nil
}
