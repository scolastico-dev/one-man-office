package settings

import (
	"maps"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// SkillSet is a git repository of skills with one text per role, stored in
// skillsets/<name>.yaml.
type SkillSet = api.SkillSet

// SkillSetRoles are the roles a skill set may have instructions for.
var SkillSetRoles = []string{"ceo", "pm", "developer", "reviewer", "export"}

// maxSkillSetText is the largest instructions text of one role, in bytes.
const maxSkillSetText = 16 << 10

func (s *Store) skillSetDir() string { return filepath.Join(s.root, "skillsets") }

func (s *Store) skillSetPath(name string) string {
	return filepath.Join(s.skillSetDir(), name+".yaml")
}

func (s *Store) skillSetKind() *kind {
	return &kind{
		section: SectionSkillSets,
		dir:     s.skillSetDir(),
		ext:     ".yaml",
		limit:   homefile.MaxSettingsFile,
		load: func(name string, data []byte) error {
			var k SkillSet
			if err := decodeYAML(data, &k); err != nil {
				return err
			}
			k.Name = name
			k = normalizeSkillSet(k)
			if err := validateSkillSet(k); err != nil {
				return err
			}
			s.skills[name] = k
			return nil
		},
		drop: func(name string) bool {
			_, ok := s.skills[name]
			delete(s.skills, name)
			return ok
		},
	}
}

// normalizeSkillSet copies the instructions (an empty map for none), drops
// the revision and fills the update mode a file leaves out (at_start).
func normalizeSkillSet(k SkillSet) SkillSet {
	k.Instructions = maps.Clone(k.Instructions)
	if k.Instructions == nil {
		k.Instructions = map[string]string{}
	}
	if k.Update == "" {
		k.Update = api.SkillSetAtStart
	}
	k.Revision = ""
	return k
}

// validateSkillSet checks a normalized skill set. The error is a *FieldError
// naming the key; its message never quotes a value.
func validateSkillSet(k SkillSet) error {
	if strings.TrimSpace(k.Repository) == "" {
		return fieldErr("repository", "repository must not be empty")
	}
	if len(k.Repository) > maxAddress {
		return fieldErr("repository", "repository must be at most %d characters long", maxAddress)
	}
	if gitops.ValidSourceURL(k.Repository) != nil {
		return fieldErr("repository", "repository must be an https:// or ssh:// address, a host:path address or an absolute folder path, without control, invisible or direction characters")
	}
	if gitops.ValidRef(k.Ref) != nil {
		return fieldErr("ref", "ref must be empty (the default branch) or a branch or tag name that does not start with - and that git accepts")
	}
	if k.Update != api.SkillSetAtStart && k.Update != api.SkillSetPinned {
		return fieldErr("update", "update must be at_start or pinned")
	}
	for _, role := range sortedKeys(k.Instructions) {
		known := false
		for _, r := range SkillSetRoles {
			known = known || r == role
		}
		if !known {
			return fieldErr("instructions", "instructions may only have the keys %s", strings.Join(SkillSetRoles, ", "))
		}
		key := "instructions." + role
		text := k.Instructions[role]
		if len(text) > maxSkillSetText {
			return fieldErr(key, "%s must be at most %d KiB", key, maxSkillSetText>>10)
		}
		if !utf8.ValidString(text) {
			return fieldErr(key, "%s must be valid UTF-8 text", key)
		}
		if hiddenInText(text) {
			return fieldErr(key, "%s must not hold control, invisible or direction characters other than line breaks and tabs", key)
		}
	}
	return nil
}

// SkillSets lists the skill sets sorted by name, with their revisions.
func (s *Store) SkillSets() []SkillSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SkillSet, 0, len(s.skills))
	for name, k := range s.skills {
		k = normalizeSkillSet(k)
		k.Revision = s.revisionLocked(s.skillSetPath(name))
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SkillSet returns one skill set by name, with its revision.
func (s *Store) SkillSet(name string) (SkillSet, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.skills[name]
	if !ok {
		return SkillSet{}, false
	}
	k = normalizeSkillSet(k)
	k.Revision = s.revisionLocked(s.skillSetPath(name))
	return k, true
}

// PutSkillSetIf creates or replaces a skill set and writes its file, guarded
// by a revision: the file is read again first, and when rev is not empty and
// differs from the file's revision the write is refused with code
// stale_settings; rev NoRevision means create only. Invalid skill sets are
// refused with code validation naming the field.
func (s *Store) PutSkillSetIf(rev string, k SkillSet) error {
	if !idPattern.MatchString(k.Name) {
		return apiError(api.CodeValidation, fieldErr("name", "the skill set name must match %s", idPattern))
	}
	k = normalizeSkillSet(k)
	if err := validateSkillSet(k); err != nil {
		return apiError(api.CodeValidation, err)
	}
	data, err := encodeYAML(k)
	if err != nil {
		return api.Errorf(api.CodeInternal, "encode skill set: %v", err)
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionSkillSets, k.Name, rev)
		if err != nil {
			return changes, err
		}
		if err := s.writeFile(s.skillSetPath(k.Name), data); err != nil {
			return changes, err
		}
		s.skills[k.Name] = k
		return append(changes, Change{Section: SectionSkillSets, Name: k.Name}), nil
	})
}

// DeleteSkillSetIf removes a skill set's file, guarded by a revision as
// PutSkillSetIf. A file that could not be loaded can be deleted too; an
// unknown skill set gives not_found. Its checkout is not the store's.
func (s *Store) DeleteSkillSetIf(rev, name string) error {
	if !idPattern.MatchString(name) {
		return api.Errorf(api.CodeNotFound, "no skill set %s", showKey(name))
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionSkillSets, name, rev)
		if err != nil {
			return changes, err
		}
		_, loaded := s.skills[name]
		path := s.skillSetPath(name)
		if !loaded && !fileExists(path) {
			return changes, api.Errorf(api.CodeNotFound, "no skill set %s", name)
		}
		if err := s.removeFile(path); err != nil {
			return changes, err
		}
		delete(s.skills, name)
		return append(changes, Change{Section: SectionSkillSets, Name: name, Removed: true}), nil
	})
}
