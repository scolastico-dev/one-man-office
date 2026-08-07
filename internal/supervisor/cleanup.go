package supervisor

import (
	"context"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

// CleanupLoop runs configured SQLite retention once at startup and then on
// the configured interval. With the default zero retentions it does nothing.
func (s *Supervisor) CleanupLoop(ctx context.Context) {
	if !s.Cfg.Cleanup.Enabled() {
		return
	}
	run := func() {
		_, err := db.Cleanup(s.DB, db.CleanupPolicy{
			ReadMessagesAfter: time.Duration(s.Cfg.Cleanup.ReadMessagesAfter),
			TerminalJobsAfter: time.Duration(s.Cfg.Cleanup.TerminalJobsAfter),
		}, time.Now())
		if err != nil {
			_ = db.AppendEvent(s.DB, "cleanup_error", "", 0, err.Error())
		}
	}
	run()
	ticker := time.NewTicker(time.Duration(s.Cfg.Cleanup.Interval))
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
