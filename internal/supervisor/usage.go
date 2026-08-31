package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
)

var ErrWeeklyUsageLimit = errors.New("weekly model usage limit reached")

func (s *Supervisor) usageEligible(role string, profiles []string) ([]string, map[string]float64, error) {
	if !s.Config().Usage.Enabled {
		return append([]string(nil), profiles...), map[string]float64{}, nil
	}
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
		scope := modelusage.Scope(profile)
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
		s.persistUsageSnapshot(snapshot)
		used[key] = snapshot.MaxUsedPercent()
		if snapshot.MaxUsedPercent() < cfg.Usage.WeeklyLimitPercent {
			eligible = append(eligible, key)
		}
	}
	return eligible, used, nil
}

func (s *Supervisor) persistUsageSnapshot(snapshot modelusage.Snapshot) {
	if s.DB == nil {
		return
	}
	_ = db.UpsertModelUsageSnapshot(s.DB, db.ModelUsageSnapshot{
		Profile:            string(snapshot.Provider),
		Provider:           string(snapshot.Provider),
		UsedPercent:        snapshot.UsedPercent,
		ResetAt:            snapshot.ResetAt,
		HasSession:         snapshot.HasSession,
		SessionUsedPercent: snapshot.SessionUsedPercent,
		SessionResetAt:     snapshot.SessionResetAt,
		FetchedAt:          snapshot.FetchedAt,
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
	if s.hasRemainingConfiguredUsage() {
		detail := fmt.Sprintf("role %s has no eligible model: %s; usage ceiling %.1f%%. The office remains running because another configured Claude/Codex credential scope still has capacity.", role, strings.Join(parts, ", "), s.Config().Usage.WeeklyLimitPercent)
		if s.DB != nil {
			_ = db.AppendEvent(s.DB, "role_usage_limit_reached", "", 0, detail)
		}
		if s.Mail != nil {
			_, _ = s.Mail.Send(bus.SystemSender, "user", "role model usage limit reached", detail, bus.PrioHigh)
		}
		return
	}
	detail := fmt.Sprintf("role %s has no eligible model: %s; usage ceiling %.1f%%. Safe shutdown is starting.", role, strings.Join(parts, ", "), s.Config().Usage.WeeklyLimitPercent)
	if s.DB != nil {
		_ = db.AppendEvent(s.DB, "weekly_usage_limit_reached", "", 0, detail)
	}
	if s.Mail != nil {
		_, _ = s.Mail.Send(bus.SystemSender, "user", "model usage limit reached", detail, bus.PrioUrgent)
	}
	if s.DB != nil {
		_ = s.BeginSafeShutdown("weekly-usage-limit")
	}
}

// hasRemainingConfiguredUsage checks every credential scope referenced by a
// role. A refresh failure conservatively keeps the office alive: unavailable
// usage data is not proof that every provider is exhausted.
func (s *Supervisor) hasRemainingConfiguredUsage() bool {
	if s.Usage == nil {
		return true
	}
	cfg := s.Config()
	seen := map[string]bool{}
	for _, role := range config.AllRoles {
		for _, key := range cfg.Roles[role].Models {
			profile := cfg.Models[key]
			if !modelusage.Metered(profile) {
				continue
			}
			scope := modelusage.Scope(profile)
			if seen[scope] {
				continue
			}
			seen[scope] = true
			timeout := time.Duration(cfg.Startup.CheckTimeout)
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			snapshot, err := s.Usage.Fetch(ctx, key, profile)
			cancel()
			if err != nil {
				s.notifyUsageFailure(err)
				return true
			}
			s.persistUsageSnapshot(snapshot)
			if snapshot.MaxUsedPercent() < cfg.Usage.WeeklyLimitPercent {
				return true
			}
		}
	}
	return false
}

func (s *Supervisor) checkExplicitProfile(profileKey string, force bool) error {
	if !s.Config().Usage.Enabled {
		return nil
	}
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
	s.persistUsageSnapshot(snapshot)
	s.clearUsageFailure()
	limit := s.Config().Usage.WeeklyLimitPercent
	window, used := snapshot.LimitingWindow()
	if used >= limit && !force {
		return fmt.Errorf("model profile %q is at %.1f%% %s usage (limit %.1f%%); rerun the command with --force to use it anyway", profileKey, used, window, limit)
	}
	return nil
}
