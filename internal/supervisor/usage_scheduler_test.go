package supervisor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
)

type scheduledUsage struct {
	mu        sync.Mutex
	refreshes int
}

func (s *scheduledUsage) Fetch(context.Context, string, config.Profile) (modelusage.Snapshot, error) {
	return modelusage.Snapshot{}, nil
}

func (s *scheduledUsage) Refresh(_ context.Context, _ string, profile config.Profile) (modelusage.Snapshot, error) {
	s.mu.Lock()
	s.refreshes++
	s.mu.Unlock()
	return modelusage.Snapshot{Provider: agentcli.Resolve(profile.Provider, profile.Cmd), UsedPercent: 20, FetchedAt: time.Now()}, nil
}

func (s *scheduledUsage) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshes
}

func TestUsageLoopRefreshesEachCredentialScope(t *testing.T) {
	usage := &scheduledUsage{}
	s := meteredAssignmentSupervisor(t, config.AssignmentRoundRobin, usage)
	s.Cfg.Usage.RefreshInterval = config.Duration(20 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.UsageLoop(ctx)
	deadline := time.Now().Add(time.Second)
	for usage.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := usage.count(); got < 2 {
		t.Fatalf("scheduled refreshes = %d, want Claude and Codex", got)
	}
}

func TestUsageLoopZeroIntervalDisablesScheduler(t *testing.T) {
	usage := &scheduledUsage{}
	s := meteredAssignmentSupervisor(t, config.AssignmentRoundRobin, usage)
	s.Cfg.Usage.RefreshInterval = 0
	ctx, cancel := context.WithCancel(context.Background())
	go s.UsageLoop(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	if got := usage.count(); got != 0 {
		t.Fatalf("disabled scheduler made %d refreshes", got)
	}
}
