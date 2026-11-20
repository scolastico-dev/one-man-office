package settings

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestRoleFilesCreatedAtOpen(t *testing.T) {
	s, home, _ := openTest(t)
	for _, name := range RoleNames {
		if !fileExists(filepath.Join(home, "roles", name+".yaml")) {
			t.Errorf("roles/%s.yaml missing", name)
		}
	}
	roles := s.Roles()
	if len(roles) != 4 {
		t.Fatalf("roles = %+v", roles)
	}
	wantAssignments := []string{"failover", "super_smart", "super_smart", "random"}
	for i, r := range roles {
		if r.Name != RoleNames[i] || r.Models == nil || len(r.Models) != 0 || r.Assignment != wantAssignments[i] {
			t.Errorf("role %d = %+v", i, r)
		}
	}
	if len(s.Errors()) != 0 {
		t.Fatalf("errors = %+v", s.Errors())
	}
}

func TestRoleRejectsUnknownModel(t *testing.T) {
	s, _, _ := openTest(t)
	wantCode(t, s.PutRoleIf("", Role{Name: "pm", Models: []string{"claude/fable"}, Assignment: api.AssignmentRandom}), api.CodeValidation)
	if err := s.PutProfileIf("", claudeProfile()); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.PutRoleIf("", Role{Name: "pm", Models: []string{"claude/fable"}, Assignment: "fastest"}), api.CodeValidation)
	wantCode(t, s.PutRoleIf("", Role{Name: "janitor"}), api.CodeNotFound)
	if err := s.PutRoleIf("", Role{Name: "pm", Models: []string{"claude/fable", "claude/fable", "claude/opus"}, Assignment: api.AssignmentRandom}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Role("pm")
	if want := []string{"claude/fable", "claude/fable", "claude/opus"}; !reflect.DeepEqual(r.Models, want) || r.Assignment != api.AssignmentRandom {
		t.Fatalf("role = %+v", r)
	}
}

func TestRoleHandEditKeepsLastValid(t *testing.T) {
	s, home, log := openTest(t)
	if err := s.PutProfileIf("", claudeProfile()); err != nil {
		t.Fatal(err)
	}
	if err := s.PutRoleIf("", Role{Name: "ceo", Models: []string{"claude/fable"}, Assignment: api.AssignmentSmart}); err != nil {
		t.Fatal(err)
	}
	log.take()
	path := filepath.Join(home, "roles", "ceo.yaml")
	writeFile(t, path, "assignment: fastest\n")
	s.Reload()
	if r, _ := s.Role("ceo"); r.Assignment != api.AssignmentSmart || len(r.Models) != 1 {
		t.Fatalf("role = %+v, want last valid", r)
	}
	if len(s.Errors()) != 1 {
		t.Fatalf("errors = %+v", s.Errors())
	}
	if c := log.take(); len(c) != 1 || c[0].Section != SectionRoles || c[0].Name != "ceo" || c[0].Err == nil {
		t.Fatalf("changes = %+v", c)
	}
	// A hand-written file with only models gets the default assignment, and
	// a stray file that is not a role is reported without a crash.
	writeFile(t, path, "models: [claude/opus]\n")
	writeFile(t, filepath.Join(home, "roles", "janitor.yaml"), "models: []\n")
	s.Reload()
	if r, _ := s.Role("ceo"); r.Assignment != api.AssignmentFailover || r.Models[0] != "claude/opus" {
		t.Fatalf("role = %+v", r)
	}
	if errs := s.Errors(); len(errs) != 1 || filepath.Base(errs[0].File) != "janitor.yaml" {
		t.Fatalf("errors = %+v", errs)
	}
	if len(s.Roles()) != 4 {
		t.Fatalf("roles = %+v", s.Roles())
	}
}

func TestRoleAssignmentDefaultsAndRoundTrip(t *testing.T) {
	s, home, _ := openTest(t)
	if err := s.PutProfileIf("", claudeProfile()); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"ceo": "failover", "pm": "super_smart", "developer": "super_smart", "reviewer": "random"} {
		t.Run(name, func(t *testing.T) {
			// API omission and a models-only YAML file use the same role default.
			if err := s.PutRoleIf("", Role{Name: name, Models: []string{"claude/opus"}}); err != nil {
				t.Fatal(err)
			}
			if r, _ := s.Role(name); r.Assignment != want {
				t.Fatalf("API default: %+v", r)
			}
			path := filepath.Join(home, "roles", name+".yaml")
			writeFile(t, path, "models: [claude/opus]\n")
			s.Reload()
			if r, _ := s.Role(name); r.Assignment != want {
				t.Fatalf("YAML default: %+v", r)
			}
			for _, mode := range []string{"super_smart", "round_robin", "smart", "random", "failover"} {
				if err := s.PutRoleIf("", Role{Name: name, Models: []string{"claude/opus"}, Assignment: mode}); err != nil {
					t.Fatal(err)
				}
				// Force a disk reload from hand-written YAML, independent of the API cache.
				writeFile(t, path, fmt.Sprintf("assignment: %s\nmodels: [claude/opus]\n", mode))
				s.Reload()
				if r, _ := s.Role(name); r.Assignment != mode {
					t.Fatalf("round trip %s: %+v", mode, r)
				}
			}
		})
	}
	if errs := s.Errors(); len(errs) != 0 {
		t.Fatalf("errors: %+v", errs)
	}
}
