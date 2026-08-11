package supervisor

import (
	"slices"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
)

func modelAssignmentSupervisor(assignment config.Assignment) *Supervisor {
	return &Supervisor{
		Cfg: &config.Config{Roles: map[string]config.RoleModels{
			"developer": {Models: []string{"alpha", "beta", "gamma"}, Assignment: assignment},
		}},
		roleModelNext: map[string]int{},
	}
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
