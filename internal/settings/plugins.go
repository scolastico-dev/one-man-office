package settings

import (
	"encoding/json"
	"math"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// PluginSettings is where a plugin comes from, whether it runs and its
// configuration, stored in plugins/<name>.yaml.
type PluginSettings = api.PluginSettings

// pluginPattern is the pattern of a plugin name. A name never starts with a
// dot, so it cannot be one of the folders the plugins folder keeps for
// itself (.cache, .data).
var pluginPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Bounds of plugin settings.
const (
	maxPluginConfig = 64 << 10
	maxSubpath      = 512
	// maxAddress is the longest repository or plugin source address.
	maxAddress = 2048
)

func (s *Store) pluginDir() string { return filepath.Join(s.root, "plugins") }

func (s *Store) pluginPath(name string) string {
	return filepath.Join(s.pluginDir(), name+".yaml")
}

func (s *Store) pluginKind() *kind {
	return &kind{
		section: SectionPlugins,
		dir:     s.pluginDir(),
		ext:     ".yaml",
		names:   pluginPattern,
		// The installed trees (plugins/<name>/) and the folders .cache and
		// .data live next to the settings files.
		skipDirs: true,
		limit:    homefile.MaxSettingsFile,
		load: func(name string, data []byte) error {
			var p PluginSettings
			if err := decodeYAML(data, &p); err != nil {
				return err
			}
			p.Name = name
			p = normalizePlugin(p)
			if err := validatePlugin(p); err != nil {
				return err
			}
			s.plugins[name] = p
			return nil
		},
		drop: func(name string) bool {
			_, ok := s.plugins[name]
			delete(s.plugins, name)
			return ok
		},
	}
}

// normalizePlugin copies the configuration (an empty object for none) and
// drops the revision.
func normalizePlugin(p PluginSettings) PluginSettings {
	p.Config = cloneConfig(p.Config)
	p.Revision = ""
	return p
}

// cloneConfig copies a configuration object deeply; nil becomes an empty
// object.
func cloneConfig(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneConfig(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	}
	return v
}

// validatePlugin checks normalized plugin settings. The error is a
// *FieldError naming the key; its message never quotes a value.
func validatePlugin(p PluginSettings) error {
	if err := validatePluginSource(p.Source); err != nil {
		return err
	}
	if err := validateSubpath(p.Subpath); err != nil {
		return err
	}
	if gitops.ValidRef(p.Ref) != nil {
		return fieldErr("ref", "ref must be empty (the default branch) or a branch or tag name that does not start with - and that git accepts")
	}
	if !jsonValue(p.Config, 0) {
		return fieldErr("config", "config must be a JSON object: keys are text, values are text, numbers, true, false, null, lists or objects (quote dates and times)")
	}
	data, err := json.Marshal(p.Config)
	if err != nil {
		return fieldErr("config", "config must be a JSON object")
	}
	if len(data) > maxPluginConfig {
		return fieldErr("config", "config must be at most %d KiB as JSON", maxPluginConfig>>10)
	}
	return nil
}

// maxConfigDepth is how deeply lists and objects of a configuration may
// nest.
const maxConfigDepth = 64

// jsonValue reports whether v is made only of the values JSON has, as the
// YAML and JSON decoders give them: objects with text keys, lists, text,
// numbers (finite), true, false and null, nested at most maxConfigDepth
// deep. A YAML object with other keys, a date or anything else is not.
func jsonValue(v any, depth int) bool {
	if depth > maxConfigDepth {
		return false
	}
	switch t := v.(type) {
	case nil, string, bool, int, int64, uint64, json.Number:
		return true
	case float64:
		return !math.IsNaN(t) && !math.IsInf(t, 0)
	case map[string]any:
		for _, e := range t {
			if !jsonValue(e, depth+1) {
				return false
			}
		}
		return true
	case []any:
		for _, e := range t {
			if !jsonValue(e, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

// validatePluginSource checks a plugin's source: an address gitops fetches
// from.
func validatePluginSource(src string) error {
	if strings.TrimSpace(src) == "" {
		return fieldErr("source", "source must not be empty")
	}
	if len(src) > maxAddress {
		return fieldErr("source", "source must be at most %d characters long", maxAddress)
	}
	if gitops.ValidSourceURL(src) != nil {
		return fieldErr("source", "source must be an https:// or ssh:// address, a host:path address or an absolute folder path, without control, invisible or direction characters")
	}
	return nil
}

// validateSubpath checks the folder of a plugin inside its repository: ""
// for the top, else a clean relative path with slashes that does not leave
// the repository.
func validateSubpath(sub string) error {
	if sub == "" {
		return nil
	}
	const msg = "subpath must be empty or a clean relative folder path with / that does not start with / and has no . or .. part"
	if len(sub) > maxSubpath {
		return fieldErr("subpath", "subpath must be at most %d characters long", maxSubpath)
	}
	if hasHidden(sub) || strings.ContainsAny(sub, `\:`) || strings.HasPrefix(sub, "/") || path.Clean(sub) != sub || sub == "." {
		return fieldErr("subpath", msg)
	}
	for _, part := range strings.Split(sub, "/") {
		if part == ".." || part == "." {
			return fieldErr("subpath", msg)
		}
	}
	return nil
}

// Plugins lists the plugin settings sorted by name, with their revisions.
func (s *Store) Plugins() []PluginSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PluginSettings, 0, len(s.plugins))
	for name, p := range s.plugins {
		p = normalizePlugin(p)
		p.Revision = s.revisionLocked(s.pluginPath(name))
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Plugin returns one plugin's settings by name, with their revision.
func (s *Store) Plugin(name string) (PluginSettings, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plugins[name]
	if !ok {
		return PluginSettings{}, false
	}
	p = normalizePlugin(p)
	p.Revision = s.revisionLocked(s.pluginPath(name))
	return p, true
}

// PutPluginIf creates or replaces a plugin's settings and writes its file,
// guarded by a revision as PutSkillSetIf (NoRevision = create only). Invalid
// settings are refused with code validation naming the field. Installing the
// plugin's tree is not the store's.
func (s *Store) PutPluginIf(rev string, p PluginSettings) error {
	if !pluginPattern.MatchString(p.Name) {
		return apiError(api.CodeValidation, fieldErr("name", "the plugin name must match %s", pluginPattern))
	}
	p = normalizePlugin(p)
	if err := validatePlugin(p); err != nil {
		return apiError(api.CodeValidation, err)
	}
	data, err := encodeYAML(p)
	if err != nil {
		return api.Errorf(api.CodeInternal, "encode plugin settings: %v", err)
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionPlugins, p.Name, rev)
		if err != nil {
			return changes, err
		}
		if err := s.writeFile(s.pluginPath(p.Name), data); err != nil {
			return changes, err
		}
		s.plugins[p.Name] = p
		return append(changes, Change{Section: SectionPlugins, Name: p.Name}), nil
	})
}

// DeletePluginIf removes a plugin's settings file, guarded by a revision as
// PutPluginIf. A file that could not be loaded can be deleted too; an
// unknown plugin gives not_found. The plugin's tree and data are not the
// store's.
func (s *Store) DeletePluginIf(rev, name string) error {
	if !pluginPattern.MatchString(name) {
		return api.Errorf(api.CodeNotFound, "no plugin %s", showKey(name))
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionPlugins, name, rev)
		if err != nil {
			return changes, err
		}
		_, loaded := s.plugins[name]
		path := s.pluginPath(name)
		if !loaded && !fileExists(path) {
			return changes, api.Errorf(api.CodeNotFound, "no plugin %s", name)
		}
		if err := s.removeFile(path); err != nil {
			return changes, err
		}
		delete(s.plugins, name)
		return append(changes, Change{Section: SectionPlugins, Name: name, Removed: true}), nil
	})
}
