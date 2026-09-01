package supervisor

import (
	"context"
	"fmt"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

// CleanupLoop runs configured SQLite and storage retention once at startup
// and then on the configured interval.
func (s *Supervisor) CleanupLoop(ctx context.Context) {
	cfg := s.Config()
	if !cfg.Cleanup.Enabled() {
		return
	}
	run := func() {
		maxEntries := cfg.Cleanup.MaxEntries
		_, err := db.Cleanup(s.DB, db.CleanupPolicy{
			ReadMessagesAfter: time.Duration(cfg.Cleanup.ReadMessagesAfter),
			TerminalJobsAfter: time.Duration(cfg.Cleanup.TerminalJobsAfter),
			StorageActiveDays: cfg.Cleanup.StorageActiveDays,
			MaxEntries: db.EntryCaps{
				Agents: maxEntries.Agents, Jobs: maxEntries.Jobs, Messages: maxEntries.Messages,
				Events: maxEntries.Events, Incidents: maxEntries.Incidents,
				OverallStatistics: maxEntries.OverallStatistics, ShutdownContexts: maxEntries.ShutdownContexts,
				ModelUsageSnapshots: maxEntries.ModelUsageSnapshots,
			},
		}, time.Now())
		if err != nil {
			_ = db.AppendEvent(s.DB, "cleanup_error", "", 0, err.Error())
		}
		removed, err := s.PruneStorage(cfg.Cleanup.StorageActiveDays)
		if err != nil {
			_ = db.AppendEvent(s.DB, "cleanup_error", "", 0, "storage: "+err.Error())
		} else if removed > 0 {
			_ = db.AppendEvent(s.DB, "storage_cleaned", "", 0, fmt.Sprintf("removed %d expired files", removed))
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
