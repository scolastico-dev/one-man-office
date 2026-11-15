package settings

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// General holds the general settings (settings.yaml). Its YAML keys and JSON
// names are equal; api.General(g) converts it to the wire type.
type General api.General

// Duration is a time span written as Go duration text.
type Duration = api.Duration

// WorkspaceRoot is a folder searched for git repositories.
type WorkspaceRoot = api.WorkspaceRoot

// defaultMaxDepth is used for a workspace root that sets no max_depth.
const defaultMaxDepth = 4

// Defaults returns the general settings used for every key a settings file
// leaves out.
func Defaults() General {
	return General{
		MaxRunningAgents:     4,
		CEO:                  api.CEOSettings{Enabled: true, MayApproveStories: false},
		Tasks:                api.TaskSettings{RequireUserRelease: false},
		ReviewDefaults:       api.ReviewDefaults{Story: api.ReviewAgent, Task: api.ReviewAgent},
		WhenApprovedDefaults: api.WhenApprovedDefaults{Story: api.WhenApprovedMerge, Task: api.WhenApprovedMerge},
		Waiting:              api.WaitingSettings{TimeoutsBeforeInvestigate: 3},
		Health: api.HealthSettings{
			SilentAfter:             Duration(15 * time.Minute),
			QuestionUnansweredAfter: Duration(30 * time.Minute),
		},
		Stop:           api.StopSettings{HandoffTimeout: Duration(2 * time.Minute)},
		WorkspaceRoots: []WorkspaceRoot{},
		RescanInterval: Duration(10 * time.Minute),
		AgentEnv: map[string]string{
			"GIT_AUTHOR_NAME":       "OMO - AI Orchestrator",
			"GIT_AUTHOR_EMAIL":      "omo@scolasti.co",
			"GIT_COMMITTER_NAME":    "${GIT_COMMITTER_NAME:${GIT_AUTHOR_NAME:`git config user.name`}}",
			"GIT_COMMITTER_EMAIL":   "${GIT_COMMITTER_EMAIL:${GIT_AUTHOR_EMAIL:`git config user.email`}}",
			"GIT_CONFIG_PARAMETERS": "'commit.gpgSign=false' ${GIT_CONFIG_PARAMETERS:-}",
		},
	}
}

// Validate checks every value. The message names the offending key.
func (g General) Validate() error {
	if g.MaxRunningAgents < 1 {
		return fmt.Errorf("max_running_agents must be at least 1, got %d", g.MaxRunningAgents)
	}
	for key, v := range map[string]api.Review{"review_defaults.story": g.ReviewDefaults.Story, "review_defaults.task": g.ReviewDefaults.Task} {
		switch v {
		case api.ReviewSkipped, api.ReviewAgent, api.ReviewHuman, api.ReviewAgentThenHuman:
		default:
			return fmt.Errorf("%s must be skipped, agent, human or agent_then_human, got %q", key, v)
		}
	}
	for key, v := range map[string]api.WhenApproved{"when_approved_defaults.story": g.WhenApprovedDefaults.Story, "when_approved_defaults.task": g.WhenApprovedDefaults.Task} {
		if v != api.WhenApprovedMerge && v != api.WhenApprovedExport {
			return fmt.Errorf("%s must be merge or export, got %q", key, v)
		}
	}
	if g.Waiting.TimeoutsBeforeInvestigate < 1 {
		return fmt.Errorf("waiting.timeouts_before_investigate must be at least 1, got %d", g.Waiting.TimeoutsBeforeInvestigate)
	}
	for _, d := range []struct {
		key string
		v   Duration
	}{
		{"health.silent_after", g.Health.SilentAfter},
		{"health.question_unanswered_after", g.Health.QuestionUnansweredAfter},
		{"stop.handoff_timeout", g.Stop.HandoffTimeout},
		{"rescan_interval", g.RescanInterval},
	} {
		if d.v <= 0 {
			return fmt.Errorf("%s must be longer than zero", d.key)
		}
	}
	for i, r := range g.WorkspaceRoots {
		if r.MaxDepth < 1 || r.MaxDepth > 16 {
			return fmt.Errorf("workspace_roots[%d].max_depth must be between 1 and 16, got %d", i, r.MaxDepth)
		}
		if !filepath.IsAbs(ExpandPath(r.Path)) {
			return fmt.Errorf("workspace_roots[%d].path must be absolute or start with ~, got %q", i, r.Path)
		}
	}
	for key := range g.AgentEnv {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("agent_env has an invalid variable name %q", key)
		}
	}
	return nil
}

// normalize fills values a writer may leave out: empty lists and maps, and
// the default depth of a workspace root.
func (g General) normalize() General {
	roots := make([]WorkspaceRoot, len(g.WorkspaceRoots))
	for i, r := range g.WorkspaceRoots {
		if r.MaxDepth == 0 {
			r.MaxDepth = defaultMaxDepth
		}
		r.Ignore = slices.Clone(r.Ignore)
		if r.Ignore == nil {
			r.Ignore = []string{}
		}
		roots[i] = r
	}
	g.WorkspaceRoots = roots
	g.AgentEnv = maps.Clone(g.AgentEnv)
	if g.AgentEnv == nil {
		g.AgentEnv = map[string]string{}
	}
	return g
}

// clone copies the slices and maps so callers cannot change the store.
func (g General) clone() General {
	roots := make([]WorkspaceRoot, len(g.WorkspaceRoots))
	for i, r := range g.WorkspaceRoots {
		r.Ignore = slices.Clone(r.Ignore)
		roots[i] = r
	}
	g.WorkspaceRoots = roots
	g.AgentEnv = maps.Clone(g.AgentEnv)
	return g
}

// General returns the general settings in effect.
func (s *Store) General() General {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.general.clone()
}

// SetGeneral validates g and writes it to settings.yaml. Invalid settings
// are rejected with code invalid_settings.
func (s *Store) SetGeneral(g General) error {
	g = g.normalize()
	if err := g.Validate(); err != nil {
		return api.Errorf(api.CodeInvalidSettings, "%v", err)
	}
	data, err := encodeYAML(api.General(g))
	if err != nil {
		return api.Errorf(api.CodeInternal, "encode settings: %v", err)
	}
	return s.mutate(func() ([]Change, error) {
		if err := s.writeFile(s.generalPath(), data); err != nil {
			return nil, err
		}
		s.general = g
		return []Change{{Section: SectionGeneral}}, nil
	})
}

func (s *Store) generalPath() string { return filepath.Join(s.root, "settings.yaml") }

func (s *Store) generalKind() *kind {
	return &kind{
		section: SectionGeneral,
		file:    s.generalPath(),
		load: func(_ string, data []byte) error {
			g := Defaults()
			g.AgentEnv = nil
			if err := decodeYAML(data, (*api.General)(&g)); err != nil {
				return err
			}
			if g.AgentEnv == nil {
				g.AgentEnv = Defaults().AgentEnv
			}
			g = g.normalize()
			if err := g.Validate(); err != nil {
				return err
			}
			s.general = g
			return nil
		},
		drop: func(string) bool {
			s.general = Defaults()
			return true
		},
	}
}

// writeDefaultGeneral writes settings.yaml with the defaults when absent.
func (s *Store) writeDefaultGeneral() error {
	path := s.generalPath()
	if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return nil
	}
	data, err := encodeYAML(api.General(Defaults()))
	if err != nil {
		return err
	}
	return s.writeFileLocked(path, data)
}

// writeFileLocked is writeFile for use before the store is shared.
func (s *Store) writeFileLocked(path string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeFile(path, data)
}

// LoadGeneral reads settings.yaml at path without creating or changing
// anything. A missing, unreadable or invalid file yields the defaults.
func LoadGeneral(path string) General {
	data, err := os.ReadFile(path)
	if err != nil {
		return Defaults()
	}
	g := Defaults()
	g.AgentEnv = nil
	if err := decodeYAML(data, (*api.General)(&g)); err != nil {
		return Defaults()
	}
	if g.AgentEnv == nil {
		g.AgentEnv = Defaults().AgentEnv
	}
	g = g.normalize()
	if g.Validate() != nil {
		return Defaults()
	}
	return g
}
