package supervisor

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestSandboxedSpawnRejectsInvalidPolicyBeforeAgentLaunch(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	profile := o.Sup.Cfg.Models["freelancer"]
	profile.Sandbox = &config.Sandbox{Enabled: true, ReadPaths: []string{filepath.Join(o.Dir, "missing")}}
	o.Sup.Cfg.Models["freelancer"] = profile
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("invalid policy spawn = %v", err)
	}
}

func TestSandboxedEarlyExitUsesConfiguredFailover(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	good := o.Sup.Cfg.Models["freelancer"]
	o.Sup.Cfg.Models["good"] = good
	o.Sup.Cfg.Models["broken"] = config.Profile{Cmd: "/bin/false", Sandbox: &config.Sandbox{Enabled: true}}
	o.Sup.Cfg.Roles["freelancer"] = config.RoleModels{Models: []string{"broken", "good"}, Assignment: config.AssignmentFailover}
	if _, err := o.Sup.SpawnConfiguredRole("freelancer", 0, o.Dir, "work", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "sandbox failure failover", func() bool {
		agents, err := db.LivingAgents(o.DB)
		if err != nil {
			return false
		}
		for _, agent := range agents {
			if agent.Profile == "good" && agent.State == "waiting" {
				return true
			}
		}
		return false
	})
}

func TestSandboxPreparationFailureUsesConfiguredFailover(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	o.Sup.Cfg.Models["good"] = o.Sup.Cfg.Models["freelancer"]
	o.Sup.Cfg.Models["broken"] = config.Profile{Cmd: "/bin/true", Sandbox: &config.Sandbox{Enabled: true, ReadPaths: []string{filepath.Join(o.Dir, "missing")}}}
	o.Sup.Cfg.Roles["freelancer"] = config.RoleModels{Models: []string{"broken", "good"}, Assignment: config.AssignmentFailover}
	if _, err := o.Sup.SpawnConfiguredRole("freelancer", 0, o.Dir, "work", 0); err != nil {
		t.Fatalf("configured failover: %v", err)
	}
	waitFor(t, 5*time.Second, "sandbox preparation failover", func() bool {
		agents, err := db.LivingAgents(o.DB)
		if err != nil {
			return false
		}
		for _, agent := range agents {
			if agent.Profile == "good" && agent.State == "waiting" {
				return true
			}
		}
		return false
	})
}

func TestSmokeAlarmSandboxFailureReleasesSpawnLockBeforeFailover(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	o := newOffice(t, map[string]string{"smokealarm": "ready\nwait\n"})
	o.Sup.Cfg.Models["good"] = o.Sup.Cfg.Models["smokealarm"]
	o.Sup.Cfg.Models["broken"] = config.Profile{Cmd: "/bin/true", Sandbox: &config.Sandbox{Enabled: true, ReadPaths: []string{filepath.Join(o.Dir, "missing")}}}
	o.Sup.Cfg.Roles["smokealarm"] = config.RoleModels{Models: []string{"broken", "good"}, Assignment: config.AssignmentFailover}
	result := make(chan error, 1)
	go func() { _, err := o.Sup.SpawnConfiguredRole("smokealarm", 0, o.Dir, "work", 0); result <- err }()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("smoke-alarm failover deadlocked")
	}
}

func TestSandboxedSpawnCanReachReadyOverOfficeSocket(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	profile := o.Sup.Cfg.Models["freelancer"]
	profile.Sandbox = &config.Sandbox{Enabled: true}
	o.Sup.Cfg.Models["freelancer"] = profile
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "sandboxed agent ready", func() bool { return agentState(t, o, name) == "waiting" })
}
