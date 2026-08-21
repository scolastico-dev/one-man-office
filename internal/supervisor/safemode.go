package supervisor

import "github.com/scolastico-dev/one-man-office/internal/db"

// EnterSafeMode allows the CEO to start while preventing every other role
// from spawning until the user or CEO deliberately resumes the office.
func (s *Supervisor) EnterSafeMode() {
	s.mu.Lock()
	s.safeMode = true
	s.ceoSpawnHalted = true
	s.mu.Unlock()
	db.AppendEvent(s.DB, "safe_mode_entered", "user", 0, "only CEO spawning is allowed")
}

// ResumeSpawning clears both a CEO spawn halt and startup safe mode, then
// wakes queued dispatch and reviews so the complete office boots immediately.
func (s *Supervisor) ResumeSpawning(actor string) {
	s.mu.Lock()
	wasSafe := s.safeMode
	s.safeMode = false
	s.ceoSpawnHalted = false
	s.mu.Unlock()
	detail := "normal spawning resumed"
	if wasSafe {
		detail = "safe mode exited; full office spawning resumed"
	}
	db.AppendEvent(s.DB, "spawning_resumed", actor, 0, detail)
	s.kickDispatch()
	go s.resumePendingReviews()
}

func (s *Supervisor) SafeMode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.safeMode
}
