package supervisor

import (
	"context"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

// CleanupLoop runs configured SQLite retention once at startup and then on
// the configured interval. With the default zero retentions it does nothing.
func (s *Supervisor) CleanupLoop(ctx context.Context) {
	cfg := s.Config()
	if !cfg.Cleanup.Enabled() {
		return
	}
	run := func() {
		_, err := db.Cleanup(s.DB, db.CleanupPolicy{
			ReadMessagesAfter: time.Duration(cfg.Cleanup.ReadMessagesAfter),
			TerminalJobsAfter: time.Duration(cfg.Cleanup.TerminalJobsAfter),
		}, time.Now())
		if err != nil {
			_ = db.AppendEvent(s.DB, "cleanup_error", "", 0, err.Error())
		}
	}
	run()
	ticker := time.NewTicker(time.Duration(cfg.Cleanup.Interval))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
