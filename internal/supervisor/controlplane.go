package supervisor

import "context"

// WatchControl requests immediate shutdown if the process owning aggregate
// capacity disappears. Standalone offices do not run this watchdog.
func (s *Supervisor) WatchControl(ctx context.Context) {
	if s.Control != nil {
		s.Control.Watch(ctx, s.controlFailed)
	}
}

func (s *Supervisor) controlFailed(err error) {
	s.mu.Lock()
	s.stopping = true
	s.exitReason = err.Error()
	s.mu.Unlock()
	s.requestEmergencyStop()
}
