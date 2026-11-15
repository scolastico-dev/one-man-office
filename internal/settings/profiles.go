package settings

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

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

// Bounds of environments, arguments and model descriptions.
const (
	maxEnvVars        = 200
	maxEnvValue       = 32 << 10
	maxArgs           = 100
	maxDescriptionLen = 200
)

// validateEnv checks the variables of the environment at the dotted key
// field. Errors name the variable (field.NAME), never its value.
func validateEnv(field string, env map[string]string) error {
	if len(env) > maxEnvVars {
		return fieldErr(field, "%s may hold at most %d variables", field, maxEnvVars)
	}
	for _, key := range sortedKeys(env) {
		if key == "" || strings.ContainsAny(key, "=\x00") || hasHidden(key) || !utf8.ValidString(key) {
			// The name is not shown: a name holding "=" may be a whole
			// NAME=value line.
			return fieldErr(field, "%s has an invalid variable name: one that is empty or holds =, a NUL byte or a control, invisible or direction character", field)
		}
		v := env[key]
		if v == api.HiddenValue {
			return fieldErr(field+"."+key, "the value of %s in %s is the placeholder for a hidden value; enter the value itself", showKey(key), field)
		}
		if len(v) > maxEnvValue {
			return fieldErr(field+"."+key, "the value of %s in %s is longer than %d KiB", showKey(key), field, maxEnvValue>>10)
		}
	}
	return nil
}

// validateArgs checks a list of arguments at the dotted key field.
func validateArgs(field string, args []string) error {
	if len(args) > maxArgs {
		return fieldErr(field, "%s may hold at most %d arguments", field, maxArgs)
	}
	return nil
}

// validateProfile checks a normalized profile. The error is a *FieldError
// naming the key; its message never quotes a value.
func validateProfile(p Profile) error {
	switch p.CLI {
	case api.CLIClaude, api.CLICodex, api.CLIGemini, api.CLICustom:
	default:
		return fieldErr("cli", "cli must be claude, codex, gemini or custom")
	}
	if strings.TrimSpace(p.Command) == "" {
		return fieldErr("command", "command must not be empty")
	}
	if err := validateArgs("args", p.Args); err != nil {
		return err
	}
	if err := validateEnv("env", p.Env); err != nil {
		return err
	}
	if hasHidden(p.AccountDir) {
		return fieldErr("account_dir", "account_dir must not hold control, invisible or direction characters")
	}
	if p.UsageScript != "" && !idPattern.MatchString(p.UsageScript) {
		return fieldErr("usage_script", "usage_script must be the name of a script matching %s", idPattern)
	}
	d := p.PromptDelivery
	if d.Delay != nil && *d.Delay < 0 {
		return fieldErr("prompt_delivery.delay", "prompt_delivery.delay must not be negative")
	}
	if d.RetryWait != nil && *d.RetryWait < 0 {
		return fieldErr("prompt_delivery.retry_wait", "prompt_delivery.retry_wait must not be negative")
	}
	if d.Retries != nil && *d.Retries < 0 {
		return fieldErr("prompt_delivery.retries", "prompt_delivery.retries must not be negative")
	}
	if len(p.Models) == 0 {
		return fieldErr("models", "a profile needs at least one model")
	}
	for _, name := range sortedKeys(p.Models) {
		m := p.Models[name]
		if !idPattern.MatchString(name) {
			return fieldErr("models", "every model name must match %s", idPattern)
		}
		key := "models." + name
		if err := validateArgs(key+".args", m.Args); err != nil {
			return err
		}
		if err := validateEnv(key+".env", m.Env); err != nil {
			return err
		}
		if err := validateDescription(key+".description", m.Description); err != nil {
			return err
		}
	}
	return nil
}

// validateDescription checks a model's description: one line of at most
// maxDescriptionLen characters without hidden characters.
func validateDescription(field, d string) error {
	if !utf8.ValidString(d) {
		return fieldErr(field, "%s must be UTF-8 text", field)
	}
	if strings.ContainsAny(d, "\n\r") {
		return fieldErr(field, "%s must be a single line", field)
	}
	if hasHidden(d) {
		return fieldErr(field, "%s must not hold control, invisible or direction characters", field)
	}
	if utf8.RuneCountInString(d) > maxDescriptionLen {
		return fieldErr(field, "%s must be at most %d characters long", field, maxDescriptionLen)
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
func (s *Store) PutProfile(p Profile) error { return s.PutProfileIf("", p) }

// PutProfileIf is PutProfile guarded by a revision: the profile's file is
// read again first, and when rev is not empty and differs from the file's
// revision the write is refused with code stale_settings. rev NoRevision
// means create only: refused when the file exists.
func (s *Store) PutProfileIf(rev string, p Profile) error {
	if !idPattern.MatchString(p.Name) {
		return apiError(api.CodeValidation, fieldErr("name", "the profile name must match %s", idPattern))
	}
	p = normalizeProfile(p)
	p.Revision = ""
	if err := validateProfile(p); err != nil {
		return apiError(api.CodeValidation, err)
	}
	data, err := encodeYAML(p)
	if err != nil {
		return api.Errorf(api.CodeInternal, "encode profile: %v", err)
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionProfiles, p.Name, rev)
		if err != nil {
			return changes, err
		}
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
			return changes, api.Errorf(api.CodeInUse, "profile %s drops models still used by the roles %s", p.Name, strings.Join(dropped, ", "))
		}
		if err := s.writeFile(s.profilePath(p.Name), data); err != nil {
			return changes, err
		}
		s.profiles[p.Name] = p
		return append(changes, Change{Section: SectionProfiles, Name: p.Name}), nil
	})
}

// DeleteProfile removes a profile's file. It fails with code in_use, naming
// the roles, while a role lists one of the profile's models. A file that
// could not be loaded can be deleted too.
func (s *Store) DeleteProfile(name string) error { return s.DeleteProfileIf("", name) }

// DeleteProfileIf is DeleteProfile guarded by a revision, as PutProfileIf.
func (s *Store) DeleteProfileIf(rev, name string) error {
	return s.mutate(func() ([]Change, error) {
		if !idPattern.MatchString(name) {
			return nil, api.Errorf(api.CodeNotFound, "no profile %s", showKey(name))
		}
		changes, err := s.guard(SectionProfiles, name, rev)
		if err != nil {
			return changes, err
		}
		_, loaded := s.profiles[name]
		path := s.profilePath(name)
		if !loaded && !fileExists(path) {
			return changes, api.Errorf(api.CodeNotFound, "no profile %s", name)
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
			return changes, api.Errorf(api.CodeInUse, "profile %s is used by the roles %s", name, strings.Join(users, ", "))
		}
		if err := s.removeFile(path); err != nil {
			return changes, err
		}
		delete(s.profiles, name)
		return append(changes, Change{Section: SectionProfiles, Name: name, Removed: true}), nil
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
