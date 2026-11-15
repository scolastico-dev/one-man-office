package settings

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// Role is the model assignment of one agent role, stored in
// roles/<role>.yaml.
type Role = api.RoleSettings

// RoleNames are the roles that have a settings file.
var RoleNames = []string{"ceo", "pm", "developer", "reviewer"}

func (s *Store) roleDir() string { return filepath.Join(s.root, "roles") }

func (s *Store) rolePath(name string) string {
	return filepath.Join(s.roleDir(), name+".yaml")
}

func (s *Store) roleKind() *kind {
	return &kind{
		section: SectionRoles,
		dir:     s.roleDir(),
		ext:     ".yaml",
		limit:   homefile.MaxSettingsFile,
		load: func(name string, data []byte) error {
			if !slices.Contains(RoleNames, name) {
				return fmt.Errorf("%s is not a role; the roles are %s", name, strings.Join(RoleNames, ", "))
			}
			r := Role{Assignment: api.AssignmentRoundRobin}
			if err := decodeYAML(data, &r); err != nil {
				return err
			}
			r.Name = name
			r = normalizeRole(r)
			// Model ids are not checked against the profiles here: a profile
			// removed by hand must not make the role file unloadable. PutRole
			// checks them. Their form is checked: an id that is not
			// "<profile>/<model>" can never name a model.
			if err := validateRoleModels(r.Models); err != nil {
				return err
			}
			if err := validateAssignment(r.Assignment); err != nil {
				return err
			}
			s.roles[name] = r
			return nil
		},
		drop: func(name string) bool {
			if !slices.Contains(RoleNames, name) {
				return false
			}
			s.roles[name] = emptyRole(name)
			return true
		},
	}
}

func emptyRole(name string) Role {
	return Role{Name: name, Models: []string{}, Assignment: api.AssignmentRoundRobin}
}

func normalizeRole(r Role) Role {
	r.Models = cloneStrings(r.Models)
	if r.Assignment == "" {
		r.Assignment = api.AssignmentRoundRobin
	}
	return r
}

func validateAssignment(a string) error {
	switch a {
	case api.AssignmentRoundRobin, api.AssignmentRandom, api.AssignmentFailover, api.AssignmentSmart:
		return nil
	}
	return fieldErr("assignment", "assignment must be round_robin, random, failover or smart")
}

// validateRoleModels checks that every entry has the form of a model id.
func validateRoleModels(ids []string) error {
	for i, id := range ids {
		profile, model, ok := strings.Cut(id, "/")
		if !ok || !idPattern.MatchString(profile) || !idPattern.MatchString(model) {
			key := "models." + strconv.Itoa(i)
			return fieldErr(key, "%s must be a model id of the form <profile>/<model>", key)
		}
	}
	return nil
}

// writeDefaultRoles writes an empty file for every role that has none.
func (s *Store) writeDefaultRoles() error {
	for _, name := range RoleNames {
		path := s.rolePath(name)
		if fileExists(path) {
			continue
		}
		data, err := encodeYAML(emptyRole(name))
		if err != nil {
			return err
		}
		if err := s.writeFileLocked(path, data); err != nil {
			return err
		}
	}
	return nil
}

// Roles lists the four roles in the order of RoleNames.
func (s *Store) Roles() []Role {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Role, 0, len(RoleNames))
	for _, name := range RoleNames {
		out = append(out, normalizeRole(s.roles[name]))
	}
	return out
}

// Role returns one role by name.
func (s *Store) Role(name string) (Role, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.roles[name]
	if !ok {
		return Role{}, false
	}
	return normalizeRole(r), true
}

// PutRole replaces a role's models and assignment mode. Unknown roles give
// not_found; unknown model ids and an invalid assignment give validation.
func (s *Store) PutRole(r Role) error { return s.PutRoleIf("", r) }

// PutRoleIf is PutRole guarded by a revision: the role's file is read again
// first, and when rev is not empty and differs from the file's revision the
// write is refused with code stale_settings.
func (s *Store) PutRoleIf(rev string, r Role) error {
	if !slices.Contains(RoleNames, r.Name) {
		return api.Errorf(api.CodeNotFound, "no role %s", showKey(r.Name))
	}
	r = normalizeRole(r)
	r.Revision = ""
	if err := validateAssignment(r.Assignment); err != nil {
		return apiError(api.CodeValidation, err)
	}
	data, err := encodeYAML(r)
	if err != nil {
		return api.Errorf(api.CodeInternal, "encode role: %v", err)
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionRoles, r.Name, rev)
		if err != nil {
			return changes, err
		}
		for i, id := range r.Models {
			if _, ok := s.modelLocked(id); !ok {
				key := "models." + strconv.Itoa(i)
				return changes, apiError(api.CodeValidation, fieldErr(key, "unknown model %s", showKey(id)))
			}
		}
		if err := s.writeFile(s.rolePath(r.Name), data); err != nil {
			return changes, err
		}
		s.roles[r.Name] = r
		return append(changes, Change{Section: SectionRoles, Name: r.Name}), nil
	})
}
