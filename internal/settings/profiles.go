package settings

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Profile is a CLI with its account and models, stored in
// profiles/<name>.yaml.
type Profile = api.Profile

// PromptDelivery controls how the start prompt reaches the CLI.
type PromptDelivery = api.PromptDelivery

// Model is a variant of a profile. A nil Selectable means selectable.
type Model = api.ProfileModel

// ModelInfo is a model flattened out of its profile; its id is
// "<profile>/<model>".
type ModelInfo = api.Model

func (s *Store) profileDir() string { return filepath.Join(s.root, "profiles") }

func (s *Store) profilePath(name string) string {
	return filepath.Join(s.profileDir(), name+".yaml")
}

func (s *Store) profileKind() *kind {
	return &kind{
		section: SectionProfiles,
		dir:     s.profileDir(),
		ext:     ".yaml",
		load: func(name string, data []byte) error {
			var p Profile
			if err := decodeYAML(data, &p); err != nil {
				return err
			}
			p.Name = name
			p = normalizeProfile(p)
			if err := validateProfile(p); err != nil {
				return err
			}
			s.profiles[name] = p
			return nil
		},
		drop: func(name string) bool {
			_, ok := s.profiles[name]
			delete(s.profiles, name)
			return ok
		},
	}
}

// normalizeProfile replaces nil lists and maps with empty ones and copies
// everything, so the result shares nothing with p.
func normalizeProfile(p Profile) Profile {
	p.Args = cloneStrings(p.Args)
	p.Env = cloneEnv(p.Env)
	d := p.PromptDelivery
	if d.Inject != nil {
		v := *d.Inject
		d.Inject = &v
	}
	if d.Delay != nil {
		v := *d.Delay
		d.Delay = &v
	}
	if d.Retries != nil {
		v := *d.Retries
		d.Retries = &v
	}
	if d.RetryWait != nil {
		v := *d.RetryWait
		d.RetryWait = &v
	}
	p.PromptDelivery = d
	models := make(map[string]Model, len(p.Models))
	for name, m := range p.Models {
		m.Args = cloneStrings(m.Args)
		m.Env = cloneEnv(m.Env)
		m.UsageWindows = cloneStrings(m.UsageWindows)
		if m.Selectable != nil {
			v := *m.Selectable
			m.Selectable = &v
		}
		models[name] = m
	}
	p.Models = models
	return p
}

func cloneStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return slices.Clone(in)
}

func cloneEnv(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	return maps.Clone(in)
}

func validateEnv(where string, env map[string]string) error {
	for key := range env {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("%s has an invalid variable name %q", where, key)
		}
	}
	return nil
}

// validateProfile checks a normalized profile. The message names the
// offending key.
func validateProfile(p Profile) error {
	switch p.CLI {
	case api.CLIClaude, api.CLICodex, api.CLIGemini, api.CLICustom:
	default:
		return fmt.Errorf("cli must be claude, codex, gemini or custom, got %q", p.CLI)
	}
	if strings.TrimSpace(p.Command) == "" {
		return fmt.Errorf("command must not be empty")
	}
	if err := validateEnv("env", p.Env); err != nil {
		return err
	}
	if p.UsageScript != "" && !idPattern.MatchString(p.UsageScript) {
		return fmt.Errorf("usage_script must be the name of a script matching %s, got %q", idPattern, p.UsageScript)
	}
	d := p.PromptDelivery
	if d.Delay != nil && *d.Delay < 0 {
		return fmt.Errorf("prompt_delivery.delay must not be negative")
	}
	if d.RetryWait != nil && *d.RetryWait < 0 {
		return fmt.Errorf("prompt_delivery.retry_wait must not be negative")
	}
	if d.Retries != nil && *d.Retries < 0 {
		return fmt.Errorf("prompt_delivery.retries must not be negative, got %d", *d.Retries)
	}
	if len(p.Models) == 0 {
		return fmt.Errorf("a profile needs at least one model")
	}
	for name, m := range p.Models {
		if !idPattern.MatchString(name) {
			return fmt.Errorf("model name %q must match %s", name, idPattern)
		}
		if err := validateEnv("models."+name+".env", m.Env); err != nil {
			return err
		}
	}
	return nil
}

// Profiles lists the profiles sorted by name.
func (s *Store) Profiles() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, normalizeProfile(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Profile returns one profile by name.
func (s *Store) Profile(name string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.profiles[name]
	if !ok {
		return Profile{}, false
	}
	return normalizeProfile(p), true
}

// PutProfile creates or replaces a profile and writes its file. Invalid
// profiles are rejected with code validation. A profile that drops a model
// which a role still lists is refused with code in_use, naming the roles and
// models.
func (s *Store) PutProfile(p Profile) error {
	if !idPattern.MatchString(p.Name) {
		return api.Errorf(api.CodeValidation, "profile name %q must match %s", p.Name, idPattern)
	}
	p = normalizeProfile(p)
	if err := validateProfile(p); err != nil {
		return api.Errorf(api.CodeValidation, "%v", err)
	}
	data, err := encodeYAML(p)
	if err != nil {
		return api.Errorf(api.CodeInternal, "encode profile: %v", err)
	}
	return s.mutate(func() ([]Change, error) {
		var dropped []string
		for _, role := range RoleNames {
			var models []string
			for _, id := range s.roles[role].Models {
				if _, model, _ := strings.Cut(id, "/"); profileOfModel(id) == p.Name {
					if _, kept := p.Models[model]; !kept {
						models = append(models, id)
					}
				}
			}
			if len(models) > 0 {
				dropped = append(dropped, fmt.Sprintf("%s (%s)", role, strings.Join(models, ", ")))
			}
		}
		if len(dropped) > 0 {
			return nil, api.Errorf(api.CodeInUse, "profile %s drops models still used by the roles %s", p.Name, strings.Join(dropped, ", "))
		}
		if err := s.writeFile(s.profilePath(p.Name), data); err != nil {
			return nil, err
		}
		s.profiles[p.Name] = p
		return []Change{{Section: SectionProfiles, Name: p.Name}}, nil
	})
}

// DeleteProfile removes a profile's file. It fails with code in_use, naming
// the roles, while a role lists one of the profile's models. A file that
// could not be loaded can be deleted too.
func (s *Store) DeleteProfile(name string) error {
	return s.mutate(func() ([]Change, error) {
		_, loaded := s.profiles[name]
		path := s.profilePath(name)
		if !idPattern.MatchString(name) || (!loaded && !fileExists(path)) {
			return nil, api.Errorf(api.CodeNotFound, "no profile %s", name)
		}
		var users []string
		for _, role := range RoleNames {
			for _, id := range s.roles[role].Models {
				if profileOfModel(id) == name {
					users = append(users, role)
					break
				}
			}
		}
		if len(users) > 0 {
			return nil, api.Errorf(api.CodeInUse, "profile %s is used by the roles %s", name, strings.Join(users, ", "))
		}
		if err := s.removeFile(path); err != nil {
			return nil, err
		}
		delete(s.profiles, name)
		return []Change{{Section: SectionProfiles, Name: name, Removed: true}}, nil
	})
}

// profileOfModel returns the profile part of a model id.
func profileOfModel(id string) string {
	profile, _, _ := strings.Cut(id, "/")
	return profile
}

// Models lists every model of every profile sorted by id.
func (s *Store) Models() []ModelInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ModelInfo{}
	for _, p := range s.profiles {
		for name := range p.Models {
			out = append(out, modelInfo(p, name))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Model returns one model by id ("<profile>/<model>").
func (s *Store) Model(id string) (ModelInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.modelLocked(id)
}

// modelLocked is Model with the state lock held.
func (s *Store) modelLocked(id string) (ModelInfo, bool) {
	profile, name, ok := strings.Cut(id, "/")
	if !ok {
		return ModelInfo{}, false
	}
	p, found := s.profiles[profile]
	if !found {
		return ModelInfo{}, false
	}
	if _, found := p.Models[name]; !found {
		return ModelInfo{}, false
	}
	return modelInfo(p, name), true
}

func modelInfo(p Profile, name string) ModelInfo {
	m := p.Models[name]
	return ModelInfo{
		ID:          p.Name + "/" + name,
		Profile:     p.Name,
		Name:        name,
		CLI:         p.CLI,
		Description: m.Description,
		Selectable:  m.Selectable == nil || *m.Selectable,
	}
}
