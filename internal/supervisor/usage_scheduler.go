package supervisor

import (
	"context"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
)

// UsageLoop proactively refreshes the shared usage cache. A zero interval
// disables scheduled requests while preserving lazy cache refreshes.
func (s *Supervisor) UsageLoop(ctx context.Context) {
	for {
		cfg := s.Config()
		interval := time.Duration(cfg.Usage.RefreshInterval)
		if !cfg.Usage.Enabled || interval <= 0 {
			interval = time.Second // periodically observe config reloads
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		cfg = s.Config()
		if cfg.Usage.Enabled && cfg.Usage.RefreshInterval > 0 {
			s.refreshUsageCache(ctx, cfg)
		}
	}
}

func (s *Supervisor) refreshUsageCache(ctx context.Context, cfg *config.Config) {
	refresher, ok := s.Usage.(modelusage.Refresher)
	if !ok {
		return
	}
	type candidate struct {
		key     string
		profile config.Profile
	}
	unique := map[string]candidate{}
	for _, role := range config.AllRoles {
		for _, key := range cfg.Roles[role].Models {
			profile := cfg.Models[key]
			if !modelusage.Metered(profile) {
				continue
			}
			scope := modelusage.Scope(profile)
			if _, exists := unique[scope]; !exists {
				unique[scope] = candidate{key: key, profile: profile}
			}
		}
	}
	for _, item := range unique {
		timeout := time.Duration(cfg.Startup.CheckTimeout)
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		fetchCtx, cancel := context.WithTimeout(ctx, timeout)
		snapshot, err := refresher.Refresh(fetchCtx, item.key, item.profile)
		cancel()
		if err != nil {
			s.notifyUsageFailure(err)
			continue
		}
		s.persistUsageSnapshot(snapshot)
		s.clearUsageFailure()
	}
	s.enforceConfiguredUsageLimits()
}
