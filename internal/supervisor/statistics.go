package supervisor

import (
	"context"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

const statisticsPersistInterval = time.Minute

// StatisticsLoop keeps the cumulative model snapshot current without adding
// one database row per sample.
func (s *Supervisor) StatisticsLoop(ctx context.Context) {
	persist := func() {
		if err := s.PersistOverallStatistics(); err != nil {
			_ = db.AppendEvent(s.DB, "statistics_error", "", 0, err.Error())
		}
	}
	persist()
	ticker := time.NewTicker(statisticsPersistInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			persist()
		}
	}
}
