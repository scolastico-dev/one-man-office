package settings

import (
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/branchname"
	"github.com/scolastico-dev/one-man-office/internal/discover"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// General holds the general settings (settings.yaml). Its YAML keys and JSON
// names are equal; api.General(g) converts it to the wire type.
type General api.General

// Duration is a time span written as Go duration text.
type Duration = api.Duration

// WorkspaceRoot is a folder searched for git repositories.
type WorkspaceRoot = api.WorkspaceRoot

// defaultMaxDepth is used for a workspace root that sets no max_depth.
const defaultMaxDepth = discover.DefaultMaxDepth

// Defaults returns the general settings used for every key a settings file
// leaves out.
func Defaults() General {
	return General{
		Limits: api.LimitSettings{Tasks: 4, Stories: 2},
		CEO:    api.CEOSettings{Enabled: true, MayApproveStories: false, TicketsNeedUserApproval: true},
		Tasks:  api.TaskSettings{RequireUserRelease: false},
		ReviewDefaults: api.ReviewDefaults{Story: api.ReviewAgentThenHuman, Task: api.ReviewAgentThenHuman,
			StoryTask: api.ReviewAgent},
		WhenApprovedDefaults: api.WhenApprovedDefaults{Story: api.WhenApprovedMerge, Task: api.WhenApprovedMerge,
			StoryTask: api.WhenApprovedMerge},
		MergeDefaults: api.MergeModes{Story: api.MergeModeMerge, Task: api.MergeModeMerge,
			StoryTask: api.MergeModeMerge},
		Waiting: api.WaitingSettings{TimeoutsBeforeInvestigate: 3},
		Health: api.HealthSettings{
			SilentAfter:             Duration(15 * time.Minute),
			QuestionUnansweredAfter: Duration(30 * time.Minute),
		},
		Images:         api.ImageSettings{KeepDaysAfterDone: DefaultImageKeepDays},
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
		Usage: api.UsageSettings{
			SoftPercent:     90,
			HardPercent:     98,
			RefreshInterval: Duration(5 * time.Minute),
			Windows:         map[string]api.UsageThreshold{},
		},
		Branches:    api.BranchSettings{Template: branchname.Default},
		PluginLists: []string{},
		MCP:         api.MCPSettings{Enabled: true, Inject: api.MCPInject{Claude: true, Codex: true}},
	}
}

// Bounds of the general settings.
const (
	maxInWorkLimit    = 64
	maxTimeoutsLimit  = 100
	maxWorkspaceRoots = 32
	maxIgnorePatterns = 100
	minHealthAfter    = time.Minute
	maxHealthAfter    = 7 * 24 * time.Hour
	minHandoffTimeout = 5 * time.Second
	maxHandoffTimeout = time.Hour
	minRescanInterval = time.Minute
	maxRescanInterval = 7 * 24 * time.Hour
	minUsageRefresh   = time.Minute
	maxUsageRefresh   = time.Hour
	maxUsageOverrides = 32
	// MaxPluginLists bounds plugin_lists; maxPluginListURL bounds one
	// address.
	MaxPluginLists   = 16
	maxPluginListURL = 2048
)

// durationText writes d as settings files do ("1m", "168h").
func durationText(d time.Duration) string {
	b, _ := Duration(d).MarshalText()
	return string(b)
}

// Validate checks every value. The error is a *FieldError naming the key; its
// message never quotes a value.
func (g General) Validate() error {
	for _, l := range []struct {
		key string
		v   int
	}{{"limits.tasks", g.Limits.Tasks}, {"limits.stories", g.Limits.Stories}} {
		if l.v < 1 || l.v > maxInWorkLimit {
			return fieldErr(l.key, "%s must be between 1 and %d", l.key, maxInWorkLimit)
		}
	}
	for _, r := range []struct {
		key string
		v   api.Review
	}{{"review_defaults.story", g.ReviewDefaults.Story}, {"review_defaults.task", g.ReviewDefaults.Task},
		{"review_defaults.story_task", g.ReviewDefaults.StoryTask}} {
		switch r.v {
		case api.ReviewSkipped, api.ReviewAgent, api.ReviewHuman, api.ReviewAgentThenHuman:
		default:
			return fieldErr(r.key, "%s must be skipped, agent, human or agent_then_human", r.key)
		}
	}
	for _, w := range []struct {
		key string
		v   api.WhenApproved
	}{{"when_approved_defaults.story", g.WhenApprovedDefaults.Story}, {"when_approved_defaults.task", g.WhenApprovedDefaults.Task}} {
		if w.v != api.WhenApprovedMerge && w.v != api.WhenApprovedExport {
			return fieldErr(w.key, "%s must be merge or export", w.key)
		}
	}
	if g.WhenApprovedDefaults.StoryTask != api.WhenApprovedMerge {
		return fieldErr("when_approved_defaults.story_task",
			"when_approved_defaults.story_task must be merge: a task inside a story is merged into its story")
	}
	for _, m := range []struct{ key, v string }{
		{"merge_defaults.story", g.MergeDefaults.Story}, {"merge_defaults.task", g.MergeDefaults.Task},
		{"merge_defaults.story_task", g.MergeDefaults.StoryTask},
	} {
		if !validMergeMode(m.v, false) {
			return fieldErr(m.key, "%s must be merge or squash", m.key)
		}
	}
	if n := g.Waiting.TimeoutsBeforeInvestigate; n < 1 || n > maxTimeoutsLimit {
		return fieldErr("waiting.timeouts_before_investigate", "waiting.timeouts_before_investigate must be between 1 and %d", maxTimeoutsLimit)
	}
	for _, d := range []struct {
		key      string
		v        Duration
		min, max time.Duration
	}{
		{"health.silent_after", g.Health.SilentAfter, minHealthAfter, maxHealthAfter},
		{"health.question_unanswered_after", g.Health.QuestionUnansweredAfter, minHealthAfter, maxHealthAfter},
		{"stop.handoff_timeout", g.Stop.HandoffTimeout, minHandoffTimeout, maxHandoffTimeout},
		{"rescan_interval", g.RescanInterval, minRescanInterval, maxRescanInterval},
		{"usage.refresh_interval", g.Usage.RefreshInterval, minUsageRefresh, maxUsageRefresh},
	} {
		if v := time.Duration(d.v); v < d.min || v > d.max {
			return fieldErr(d.key, "%s must be between %s and %s", d.key, durationText(d.min), durationText(d.max))
		}
	}
	if len(g.WorkspaceRoots) > maxWorkspaceRoots {
		return fieldErr("workspace_roots", "workspace_roots may hold at most %d folders", maxWorkspaceRoots)
	}
	for i, r := range g.WorkspaceRoots {
		key := "workspace_roots." + strconv.Itoa(i)
		if r.MaxDepth < 1 || r.MaxDepth > discover.MaxMaxDepth {
			return fieldErr(key+".max_depth", "%s.max_depth must be between 1 and %d", key, discover.MaxMaxDepth)
		}
		if !filepath.IsAbs(ExpandPath(r.Path)) {
			return fieldErr(key+".path", "%s.path must be absolute or start with ~", key)
		}
		if hasHidden(r.Path) {
			return fieldErr(key+".path", "%s.path must not hold control, invisible or direction characters", key)
		}
		if len(r.Ignore) > maxIgnorePatterns {
			return fieldErr(key+".ignore", "%s.ignore may hold at most %d patterns", key, maxIgnorePatterns)
		}
		for j, p := range r.Ignore {
			pkey := key + ".ignore." + strconv.Itoa(j)
			if hasHidden(p) {
				return fieldErr(pkey, "%s must not hold control, invisible or direction characters", pkey)
			}
			if discover.ValidPattern(p) != nil {
				return fieldErr(pkey, "%s must be a folder name or a path below the root, may use * ? and [ ], must not use ** and must not start or end with /", pkey)
			}
		}
	}
	if err := validateImages(g.Images); err != nil {
		return err
	}
	if err := validateUsage(g.Usage); err != nil {
		return err
	}
	if err := branchname.Check(g.Branches.Template); err != nil {
		return fieldErr("branches.template", "%s", err.Error())
	}
	if err := validatePluginLists(g.PluginLists); err != nil {
		return err
	}
	return validateEnv("agent_env", g.AgentEnv)
}

// validatePluginLists checks the addresses of the additional plugin lists:
// at most MaxPluginLists, each an absolute http:// or https:// address with
// a host, without user name, password or #fragment, of at most
// maxPluginListURL characters without hidden characters, none twice. omo
// fetches a list file from there as it is; no other transport is taken.
func validatePluginLists(lists []string) error {
	if len(lists) > MaxPluginLists {
		return fieldErr("plugin_lists", "plugin_lists may hold at most %d addresses", MaxPluginLists)
	}
	first := map[string]int{}
	for i, raw := range lists {
		key := "plugin_lists." + strconv.Itoa(i)
		if hasHidden(raw) {
			return fieldErr(key, "%s must not hold control, invisible or direction characters", key)
		}
		if len(raw) > maxPluginListURL {
			return fieldErr(key, "%s must be at most %d characters", key, maxPluginListURL)
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
			return fieldErr(key, "%s must be an http:// or https:// address of a plugin list file", key)
		}
		if u.Host == "" || u.Hostname() == "" {
			return fieldErr(key, "%s must name a host", key)
		}
		if u.User != nil {
			return fieldErr(key, "%s must not hold a user name or password", key)
		}
		if u.Fragment != "" || strings.Contains(raw, "#") {
			return fieldErr(key, "%s must not hold a # part", key)
		}
		if j, seen := first[raw]; seen {
			return fieldErr(key, "%s repeats plugin_lists.%d", key, j)
		}
		first[raw] = i
	}
	return nil
}

// percentIn reports whether v lies in [min, 100]; NaN does not.
func percentIn(v, min float64) bool { return v >= min && v <= 100 }

// validateUsage checks the usage thresholds: soft 1–100, hard from soft to
// 100, at most maxUsageOverrides windows keyed by a window name (that window
// of every script) or "<script>/<window>" (that script's window only), and
// each window's thresholds as the general ones, a value it leaves out being
// the bare name's (for a scoped key), else the general one. A field error
// names the key as it is: usage.windows.claude/weekly.soft_percent. The interval is checked with the other durations.
func validateUsage(u api.UsageSettings) error {
	if !percentIn(u.SoftPercent, 1) {
		return fieldErr("usage.soft_percent", "usage.soft_percent must be between 1 and 100")
	}
	if !percentIn(u.HardPercent, u.SoftPercent) {
		return fieldErr("usage.hard_percent", "usage.hard_percent must be between usage.soft_percent and 100")
	}
	if len(u.Windows) > maxUsageOverrides {
		return fieldErr("usage.windows", "usage.windows may hold at most %d windows", maxUsageOverrides)
	}
	for _, name := range sortedKeys(u.Windows) {
		script, window, scoped := strings.Cut(name, "/")
		if !scoped {
			script, window = "", name
		}
		if !windowPattern.MatchString(window) || (scoped && !idPattern.MatchString(script)) {
			return fieldErr("usage.windows", "every key in usage.windows must be a window name matching %s, or a script name, \"/\" and a window name", windowPattern)
		}
		key := "usage.windows." + name
		w := u.Windows[name]
		// A scoped key's values default to the bare name's, then the
		// general ones; a bare name's to the general ones.
		soft, hard := u.SoftPercent, u.HardPercent
		if bare, ok := u.Windows[window]; ok && scoped {
			if bare.SoftPercent != nil {
				soft = *bare.SoftPercent
			}
			if bare.HardPercent != nil {
				hard = *bare.HardPercent
			}
		}
		if w.SoftPercent != nil {
			soft = *w.SoftPercent
			if !percentIn(soft, 1) {
				return fieldErr(key+".soft_percent", "%s.soft_percent must be between 1 and 100", key)
			}
		}
		if w.HardPercent != nil {
			hard = *w.HardPercent
			if !percentIn(hard, soft) {
				return fieldErr(key+".hard_percent", "%s.hard_percent must be between the window's soft threshold and 100", key)
			}
		} else if hard < soft {
			if scoped {
				return fieldErr(key+".soft_percent", "%s.soft_percent must not be above the window's hard threshold", key)
			}
			return fieldErr(key+".soft_percent", "%s.soft_percent must not be above usage.hard_percent, the window's hard threshold", key)
		}
	}
	return nil
}

// cloneUsage copies the window thresholds, so callers cannot change the
// store; a nil map becomes an empty one.
func cloneUsage(u api.UsageSettings) api.UsageSettings {
	windows := make(map[string]api.UsageThreshold, len(u.Windows))
	for name, w := range u.Windows {
		if w.SoftPercent != nil {
			v := *w.SoftPercent
			w.SoftPercent = &v
		}
		if w.HardPercent != nil {
			v := *w.HardPercent
			w.HardPercent = &v
		}
		windows[name] = w
	}
	u.Windows = windows
	return u
}

// rootKey is the folder a workspace root names, for comparing roots: ~
// expanded and the path cleaned. Links are not resolved.
func rootKey(path string) string { return filepath.Clean(ExpandPath(path)) }

// newRepeats refuses a workspace root that names the same folder as an
// earlier root, and an ignore pattern that one root lists twice. The error is
// a *FieldError naming the later entry. A repeat that prev (the settings in
// effect) already has is allowed: a hand-written file with repeats loads as it
// is (Validate does not check repeats) and can still be saved, but a write
// adds no new repeat.
func newRepeats(g, prev General) error {
	hadRoot := map[string]bool{}
	hadPattern := map[[2]string]bool{}
	seenRoots := map[string]bool{}
	for _, r := range prev.WorkspaceRoots {
		key := rootKey(r.Path)
		if seenRoots[key] {
			hadRoot[key] = true
		}
		seenRoots[key] = true
		seen := map[string]bool{}
		for _, p := range r.Ignore {
			if seen[p] {
				hadPattern[[2]string{key, p}] = true
			}
			seen[p] = true
		}
	}
	first := map[string]int{}
	for i, r := range g.WorkspaceRoots {
		key := rootKey(r.Path)
		field := "workspace_roots." + strconv.Itoa(i)
		if j, seen := first[key]; seen && !hadRoot[key] {
			return fieldErr(field+".path", "%s.path is the same folder as workspace_roots.%d.path", field, j)
		} else if !seen {
			first[key] = i
		}
		firstPattern := map[string]int{}
		for j, p := range r.Ignore {
			if k, seen := firstPattern[p]; seen && !hadPattern[[2]string{key, p}] {
				return fieldErr(field+".ignore."+strconv.Itoa(j), "%s.ignore.%d repeats %s.ignore.%d", field, j, field, k)
			} else if !seen {
				firstPattern[p] = j
			}
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
	g.Usage = cloneUsage(g.Usage)
	g.PluginLists = slices.Clone(g.PluginLists)
	if g.PluginLists == nil {
		g.PluginLists = []string{}
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
	g.Usage = cloneUsage(g.Usage)
	g.PluginLists = slices.Clone(g.PluginLists)
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
func (s *Store) SetGeneral(g General) error { return s.SetGeneralIf("", g) }

// SetGeneralIf is SetGeneral guarded by a revision: settings.yaml is read
// again first, and when rev is not empty and differs from the file's
// revision the write is refused with code stale_settings.
func (s *Store) SetGeneralIf(rev string, g General) error {
	g = g.normalize()
	if err := g.Validate(); err != nil {
		return apiError(api.CodeInvalidSettings, err)
	}
	data, err := encodeYAML(api.General(g))
	if err != nil {
		return api.Errorf(api.CodeInternal, "encode settings: %v", err)
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionGeneral, "", rev)
		if err != nil {
			return changes, err
		}
		if err := newRepeats(g, s.general); err != nil {
			return changes, apiError(api.CodeInvalidSettings, err)
		}
		if err := s.writeFile(s.generalPath(), data); err != nil {
			return changes, err
		}
		s.general = g
		return append(changes, Change{Section: SectionGeneral}), nil
	})
}

// UpdateGeneral changes the general settings in place: under the store's
// lock, after settings.yaml was read again and rev checked as in
// SetGeneralIf, change runs on a copy of the settings in effect; the result is
// validated and written. Nothing is written when change fails (its error is
// returned as it is) or the result is invalid (code invalid_settings). It
// returns the settings written.
func (s *Store) UpdateGeneral(rev string, change func(*General) error) (General, error) {
	var out General
	err := s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionGeneral, "", rev)
		if err != nil {
			return changes, err
		}
		g := s.general.clone()
		if err := change(&g); err != nil {
			return changes, err
		}
		g = g.normalize()
		if err := g.Validate(); err != nil {
			return changes, apiError(api.CodeInvalidSettings, err)
		}
		if err := newRepeats(g, s.general); err != nil {
			return changes, apiError(api.CodeInvalidSettings, err)
		}
		data, err := encodeYAML(api.General(g))
		if err != nil {
			return changes, api.Errorf(api.CodeInternal, "encode settings: %v", err)
		}
		if err := s.writeFile(s.generalPath(), data); err != nil {
			return changes, err
		}
		s.general = g
		out = g.clone()
		return append(changes, Change{Section: SectionGeneral}), nil
	})
	if err != nil {
		return General{}, err
	}
	return out, nil
}

func (s *Store) generalPath() string { return filepath.Join(s.root, "settings.yaml") }

func (s *Store) generalKind() *kind {
	return &kind{
		section: SectionGeneral,
		file:    s.generalPath(),
		limit:   homefile.MaxSettingsFile,
		load: func(_ string, data []byte) error {
			g, err := decodeGeneral(data)
			if err != nil {
				return err
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

// decodeGeneral reads the content of settings.yaml over the defaults. A
// file without agent_env gets the default environment.
func decodeGeneral(data []byte) (General, error) {
	d := Defaults()
	g := d
	g.AgentEnv = nil
	if err := decodeYAML(data, (*api.General)(&g)); err != nil {
		return General{}, err
	}
	if g.AgentEnv == nil {
		g.AgentEnv = d.AgentEnv
	}
	return g, nil
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
	data, err := homefile.Read(path, homefile.MaxSettingsFile)
	if err != nil {
		return Defaults()
	}
	g, err := decodeGeneral(data)
	if err != nil {
		return Defaults()
	}
	g = g.normalize()
	if g.Validate() != nil {
		return Defaults()
	}
	return g
}
