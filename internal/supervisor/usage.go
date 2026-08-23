package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
)

var ErrWeeklyUsageLimit = errors.New("weekly model usage limit reached")

func (s *Supervisor) usageEligible(role string, profiles []string) ([]string, map[string]float64, error) {
	s.roleModelMu.Lock()
	defer s.roleModelMu.Unlock()
	cfg := s.Config()
	used := make(map[string]float64, len(profiles))
	eligible := make([]string, 0, len(profiles))
	cache := map[string]modelusage.Snapshot{}
	for _, key := range profiles {
		profile, ok := cfg.Models[key]
		if !ok || !modelusage.Metered(profile) {
			eligible = append(eligible, key)
			continue
		}
		if s.Usage == nil {
			return nil, nil, fmt.Errorf("usage client is unavailable for metered profile %q", key)
		}
		scope := modelusage.Scope(profile) + "\x00" + strings.Join(profile.Args, "\x00")
		snapshot, ok := cache[scope]
		if !ok {
			timeout := time.Duration(cfg.Startup.CheckTimeout)
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			var err error
			snapshot, err = s.Usage.Fetch(ctx, key, profile)
			cancel()
			if err != nil {
				return nil, nil, fmt.Errorf("refresh %s: %w", key, err)
			}
			cache[scope] = snapshot
		}
		s.persistUsageSnapshot(key, snapshot)
		used[key] = snapshot.UsedPercent
		if snapshot.UsedPercent < cfg.Usage.WeeklyLimitPercent {
			eligible = append(eligible, key)
		}
	}
	return eligible, used, nil
}

func (s *Supervisor) persistUsageSnapshot(profile string, snapshot modelusage.Snapshot) {
	if s.DB == nil {
		return
	}
	_ = db.UpsertModelUsageSnapshot(s.DB, db.ModelUsageSnapshot{
		Profile:     profile,
		Provider:    string(snapshot.Provider),
		UsedPercent: snapshot.UsedPercent,
		ResetAt:     snapshot.ResetAt,
		FetchedAt:   snapshot.FetchedAt,
	})
}

func (s *Supervisor) notifyUsageFailure(err error) {
	s.roleModelMu.Lock()
	if s.usageFailureActive {
		s.roleModelMu.Unlock()
		return
	}
	s.usageFailureActive = true
	s.roleModelMu.Unlock()
	detail := "Model usage refresh failed; this spawn is using round-robin fallback. " + err.Error()
	if s.DB != nil {
		_ = db.AppendEvent(s.DB, "model_usage_refresh_failed", "", 0, detail)
	}
	if s.Mail != nil {
		_, _ = s.Mail.Send(bus.SystemSender, "user", "model usage check failed", detail, bus.PrioHigh)
	}
}

func (s *Supervisor) clearUsageFailure() {
	s.roleModelMu.Lock()
	s.usageFailureActive = false
	s.roleModelMu.Unlock()
}

func (s *Supervisor) beginUsageLimitShutdown(role string, profiles []string, used map[string]float64) {
	parts := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		parts = append(parts, fmt.Sprintf("%s %.1f%%", profile, used[profile]))
	}
	sort.Strings(parts)
	detail := fmt.Sprintf("role %s has no eligible model: %s; weekly ceiling %.1f%%. Safe shutdown is starting.", role, strings.Join(parts, ", "), s.Config().Usage.WeeklyLimitPercent)
	if s.DB != nil {
		_ = db.AppendEvent(s.DB, "weekly_usage_limit_reached", "", 0, detail)
	}
	if s.Mail != nil {
		_, _ = s.Mail.Send(bus.SystemSender, "user", "weekly model usage limit reached", detail, bus.PrioUrgent)
	}
	if s.DB != nil {
		_ = s.BeginSafeShutdown("weekly-usage-limit")
	}
}

func (s *Supervisor) checkExplicitProfile(profileKey string, force bool) error {
	profile, ok := s.Config().Models[profileKey]
	if !ok || !modelusage.Metered(profile) {
		return nil
	}
	if s.Usage == nil {
		return nil
	}
	timeout := time.Duration(s.Config().Startup.CheckTimeout)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	snapshot, err := s.Usage.Fetch(ctx, profileKey, profile)
	cancel()
	if err != nil {
		s.notifyUsageFailure(err)
		return nil
	}
	s.persistUsageSnapshot(profileKey, snapshot)
	s.clearUsageFailure()
	limit := s.Config().Usage.WeeklyLimitPercent
	if snapshot.UsedPercent >= limit && !force {
		return fmt.Errorf("model profile %q is at %.1f%% weekly usage (limit %.1f%%); rerun the command with --force to use it anyway", profileKey, snapshot.UsedPercent, limit)
	}
	return nil
}
