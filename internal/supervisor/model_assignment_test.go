package supervisor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
)

type assignmentUsage struct {
	used  map[string]float64
	err   error
	calls int
}

func meteredAssignmentSupervisor(t *testing.T, assignment config.Assignment, fetcher modelusage.Fetcher) *Supervisor {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	cfg := config.Defaults()
	cfg.Models = map[string]config.Profile{
		"alpha": {Cmd: "claude", Args: []string{"--model", "alpha"}},
		"beta":  {Cmd: "codex", Args: []string{"--model", "beta"}},
	}
	cfg.Roles = map[string]config.RoleModels{"developer": {Models: []string{"alpha", "beta"}, Assignment: assignment}}
	s := New(&cfg, d, gitops.New(), t.TempDir(), nil)
	s.Usage = fetcher
	return s
}

func (f *assignmentUsage) Fetch(_ context.Context, key string, _ config.Profile) (modelusage.Snapshot, error) {
	f.calls++
	if f.err != nil {
		return modelusage.Snapshot{}, f.err
	}
	return modelusage.Snapshot{UsedPercent: f.used[key]}, nil
}

func modelAssignmentSupervisor(assignment config.Assignment) *Supervisor {
	return &Supervisor{
		Cfg: &config.Config{Roles: map[string]config.RoleModels{
			"developer": {Models: []string{"alpha", "beta", "gamma"}, Assignment: assignment},
		}, Usage: config.Usage{Enabled: true}},
		roleModelNext: map[string]int{},
	}
}

func TestRoleProfileSmartChoosesHighestRemainingWithStableTie(t *testing.T) {
	fetcher := &assignmentUsage{used: map[string]float64{"alpha": 50, "beta": 40, "gamma": 40}}
	s := modelAssignmentSupervisor(config.AssignmentSmart)
	s.Cfg.Usage.WeeklyLimitPercent = 90
	s.Cfg.Models = map[string]config.Profile{
		"alpha": {Cmd: "claude", Args: []string{"--model", "alpha"}},
		"beta":  {Cmd: "codex", Args: []string{"--model", "beta"}},
		"gamma": {Cmd: "codex", Args: []string{"--model", "gamma"}},
	}
	s.Usage = fetcher
	got, err := s.roleProfile("developer", 0)
	if err != nil || got != "beta" {
		t.Fatalf("smart selection = %q, %v; want beta", got, err)
	}
	if fetcher.calls != 3 {
		t.Fatalf("usage fetches = %d, want one per candidate model window", fetcher.calls)
	}
}

func TestDisabledUsageSmartAssignmentUsesRoundRobinWithoutFetching(t *testing.T) {
	fetcher := &assignmentUsage{used: map[string]float64{"alpha": 20, "beta": 10}}
	s := meteredAssignmentSupervisor(t, config.AssignmentSmart, fetcher)
	s.Cfg.Usage.Enabled = false
	for i, want := range []string{"alpha", "beta", "alpha"} {
		got, err := s.roleProfile("developer", 0)
		if err != nil || got != want {
			t.Fatalf("disabled usage selection %d = %q, %v; want %q", i, got, err, want)
		}
	}
	if fetcher.calls != 0 {
		t.Fatalf("disabled usage assignment made %d calls", fetcher.calls)
	}
}

func TestDisabledUsageSkipsExplicitProfileCheck(t *testing.T) {
	fetcher := &assignmentUsage{used: map[string]float64{"alpha": 95}}
	s := meteredAssignmentSupervisor(t, config.AssignmentRoundRobin, fetcher)
	s.Cfg.Usage.Enabled = false
	if err := s.checkExplicitProfile("alpha", false); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("disabled explicit check made %d calls", fetcher.calls)
	}
}

func TestDisabledUsageResetsSmartRefreshFailureNotificationStreak(t *testing.T) {
	fetcher := &assignmentUsage{err: fmt.Errorf("temporary outage")}
	s := meteredAssignmentSupervisor(t, config.AssignmentSmart, fetcher)
	if _, err := s.roleProfile("developer", 0); err != nil {
		t.Fatal(err)
	}
	disabled := *s.Config()
	disabled.Usage.Enabled = false
	s.replaceConfig(&disabled)
	if _, err := s.roleProfile("developer", 0); err != nil {
		t.Fatal(err)
	}
	enabled := *s.Config()
	enabled.Usage.Enabled = true
	s.replaceConfig(&enabled)
	if _, err := s.roleProfile("developer", 0); err != nil {
		t.Fatal(err)
	}
	messages, _ := s.Mail.History()
	if countSubject(messages, "model usage check failed") != 2 {
		t.Fatalf("usage toggle did not start a new failure notification streak: %#v", messages)
	}
}

func TestRoleProfilePersistsLastSuccessfulUsageChecks(t *testing.T) {
	fetcher := &assignmentUsage{used: map[string]float64{"alpha": 52, "beta": 64}}
	s := meteredAssignmentSupervisor(t, config.AssignmentSmart, fetcher)
	if _, err := s.roleProfile("developer", 0); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ModelUsageSnapshots(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Profile != "alpha" || rows[0].UsedPercent != 52 || rows[1].Profile != "beta" || rows[1].UsedPercent != 64 {
		t.Fatalf("persisted usage snapshots = %+v", rows)
	}
}

func TestRoleProfileFiltersWeeklyCappedProfilesForRoundRobin(t *testing.T) {
	s := modelAssignmentSupervisor(config.AssignmentRoundRobin)
	s.Cfg.Usage.WeeklyLimitPercent = 90
	s.Cfg.Models = map[string]config.Profile{
		"alpha": {Cmd: "claude", Args: []string{"--model", "alpha"}},
		"beta":  {Cmd: "codex", Args: []string{"--model", "beta"}},
		"gamma": {Cmd: "codex", Args: []string{"--model", "gamma"}},
	}
	s.Usage = &assignmentUsage{used: map[string]float64{"alpha": 95, "beta": 30, "gamma": 91}}
	for i := range 3 {
		got, err := s.roleProfile("developer", 0)
		if err != nil || got != "beta" {
			t.Fatalf("selection %d = %q, %v; want beta", i, got, err)
		}
	}
}

func TestRoleProfileUsageFailureFallsBackToRoundRobin(t *testing.T) {
	s := modelAssignmentSupervisor(config.AssignmentSmart)
	s.Cfg.Usage.WeeklyLimitPercent = 90
	s.Cfg.Models = map[string]config.Profile{
		"alpha": {Cmd: "claude"}, "beta": {Cmd: "codex"}, "gamma": {Cmd: "codex"},
	}
	s.Usage = &assignmentUsage{err: fmt.Errorf("temporary outage")}
	want := []string{"alpha", "beta", "gamma"}
	for i, expected := range want {
		got, err := s.roleProfile("developer", 0)
		if err != nil || got != expected {
			t.Fatalf("fallback %d = %q, %v; want %q", i, got, err, expected)
		}
	}
}

func TestUsageFailureNotifiesOncePerFailureStreak(t *testing.T) {
	fetcher := &assignmentUsage{err: fmt.Errorf("temporary outage"), used: map[string]float64{"alpha": 10, "beta": 20}}
	s := meteredAssignmentSupervisor(t, config.AssignmentSmart, fetcher)
	for range 3 {
		if _, err := s.roleProfile("developer", 0); err != nil {
			t.Fatal(err)
		}
	}
	messages, _ := s.Mail.History()
	if countSubject(messages, "model usage check failed") != 1 {
		t.Fatalf("failure streak messages = %#v", messages)
	}
	fetcher.err = nil
	if _, err := s.roleProfile("developer", 0); err != nil {
		t.Fatal(err)
	}
	fetcher.err = fmt.Errorf("outage again")
	if _, err := s.roleProfile("developer", 0); err != nil {
		t.Fatal(err)
	}
	messages, _ = s.Mail.History()
	if countSubject(messages, "model usage check failed") != 2 {
		t.Fatalf("second failure streak was not reported: %#v", messages)
	}
}

func TestAllCappedProfilesStartSafeShutdownWithUserNote(t *testing.T) {
	s := meteredAssignmentSupervisor(t, config.AssignmentSmart, &assignmentUsage{used: map[string]float64{"alpha": 95, "beta": 99}})
	_, err := s.roleProfile("developer", 0)
	if !errors.Is(err, ErrWeeklyUsageLimit) {
		t.Fatalf("all-capped error = %v", err)
	}
	messages, _ := s.Mail.History()
	if countSubject(messages, "weekly model usage limit reached") != 1 {
		t.Fatalf("weekly limit note missing: %#v", messages)
	}
	events, _ := db.AllEvents(s.DB)
	found := false
	for _, event := range events {
		found = found || event.Kind == "weekly_usage_limit_reached"
	}
	if !found {
		t.Fatalf("weekly limit event missing: %#v", events)
	}
}

func TestExplicitProfileRequiresForceAboveWeeklyLimit(t *testing.T) {
	fetcher := &assignmentUsage{used: map[string]float64{"alpha": 95, "beta": 10}}
	s := meteredAssignmentSupervisor(t, config.AssignmentRoundRobin, fetcher)
	err := s.checkExplicitProfile("alpha", false)
	if err == nil || !strings.Contains(err.Error(), "95.0%") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("rejection = %v", err)
	}
	before := fetcher.calls
	if err := s.checkExplicitProfile("alpha", true); err != nil {
		t.Fatalf("forced profile rejected: %v", err)
	}
	if fetcher.calls != before+1 {
		t.Fatal("forced spawn did not refresh usage")
	}
}

func TestDirectSpawnChecksWeeklyLimitImmediatelyBeforeLaunch(t *testing.T) {
	s := meteredAssignmentSupervisor(t, config.AssignmentRoundRobin, &assignmentUsage{used: map[string]float64{"alpha": 95, "beta": 10}})
	if _, err := s.Spawn("developer", "alpha", 0, t.TempDir(), "blocked"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("direct capped spawn error = %v", err)
	}
	agents, err := db.LivingAgents(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 0 {
		t.Fatalf("capped spawn created agents: %#v", agents)
	}
}

func countSubject(messages []bus.Message, subject string) int {
	count := 0
	for _, message := range messages {
		if message.Subject == subject {
			count++
		}
	}
	return count
}

func TestRoleProfileRoundRobin(t *testing.T) {
	s := modelAssignmentSupervisor(config.AssignmentRoundRobin)
	want := []string{"alpha", "beta", "gamma", "alpha"}
	for i, expected := range want {
		got, err := s.roleProfile("developer", 0)
		if err != nil || got != expected {
			t.Fatalf("selection %d = %q, %v; want %q", i, got, err, expected)
		}
	}
}

func TestRoleProfileFailoverUsesRetries(t *testing.T) {
	s := modelAssignmentSupervisor(config.AssignmentFailover)
	want := []string{"alpha", "beta", "gamma", "gamma"}
	for retries, expected := range want {
		got, err := s.roleProfile("developer", retries)
		if err != nil || got != expected {
			t.Fatalf("retries %d = %q, %v; want %q", retries, got, err, expected)
		}
	}
}

func TestRoleProfileRandomUsesConfiguredSet(t *testing.T) {
	s := modelAssignmentSupervisor(config.AssignmentRandom)
	for range 20 {
		got, err := s.roleProfile("developer", 0)
		if err != nil || !slices.Contains([]string{"alpha", "beta", "gamma"}, got) {
			t.Fatalf("random selection = %q, %v", got, err)
		}
	}
}
