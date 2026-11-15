package settings

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/scolastico-dev/one-man-office/internal/api"
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
		load: func(name string, data []byte) error {
			if !slices.Contains(RoleNames, name) {
				return fmt.Errorf("%q is not a role; the roles are %v", name, RoleNames)
			}
			r := Role{Assignment: api.AssignmentRoundRobin}
			if err := decodeYAML(data, &r); err != nil {
				return err
			}
			r.Name = name
			r = normalizeRole(r)
			// Model ids are not checked against the profiles here: a profile
			// removed by hand must not make the role file unloadable. PutRole
			// checks them.
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
	return fmt.Errorf("assignment must be round_robin, random, failover or smart, got %q", a)
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
func (s *Store) PutRole(r Role) error {
	if !slices.Contains(RoleNames, r.Name) {
		return api.Errorf(api.CodeNotFound, "no role %s", r.Name)
	}
	r = normalizeRole(r)
	if err := validateAssignment(r.Assignment); err != nil {
		return api.Errorf(api.CodeValidation, "%v", err)
	}
	data, err := encodeYAML(r)
	if err != nil {
		return api.Errorf(api.CodeInternal, "encode role: %v", err)
	}
	return s.mutate(func() ([]Change, error) {
		for _, id := range r.Models {
			if _, ok := s.modelLocked(id); !ok {
				return nil, api.Errorf(api.CodeValidation, "unknown model %q", id)
			}
		}
		if err := s.writeFile(s.rolePath(r.Name), data); err != nil {
			return nil, err
		}
		s.roles[r.Name] = r
		return []Change{{Section: SectionRoles, Name: r.Name}}, nil
	})
}
