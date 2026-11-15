package engine

import (
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// fakeCatalog knows the models it lists.
type fakeCatalog map[string]bool

func (c fakeCatalog) Model(id string) (settings.ModelInfo, bool) {
	if !c[id] {
		return settings.ModelInfo{}, false
	}
	profile, name, _ := strings.Cut(id, "/")
	return settings.ModelInfo{ID: id, Profile: profile, Name: name}, true
}

func role(name, mode string, models ...string) settings.Role {
	return settings.Role{Name: name, Models: models, Assignment: mode}
}

func mustPick(t *testing.T, p *picker, r api.Role, c modelChoice, cat modelCatalog) string {
	t.Helper()
	got, err := p.pick(r, c, cat)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	return got
}

func TestPickModel(t *testing.T) {
	cat := fakeCatalog{"f/a": true, "f/b": true, "f/c": true}
	rr := role("developer", api.AssignmentRoundRobin, "f/a", "f/b")

	t.Run("ticket model wins", func(t *testing.T) {
		p := newPicker(nil)
		if got := mustPick(t, p, api.RoleDeveloper, modelChoice{ticketModel: "f/c", previous: "f/b", role: rr}, cat); got != "f/c" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("missing ticket model falls through", func(t *testing.T) {
		p := newPicker(nil)
		if got := mustPick(t, p, api.RoleDeveloper, modelChoice{ticketModel: "gone/x", previous: "f/b", role: rr}, cat); got != "f/b" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("previous model reused when it exists", func(t *testing.T) {
		p := newPicker(nil)
		if got := mustPick(t, p, api.RoleDeveloper, modelChoice{previous: "f/b", role: rr}, cat); got != "f/b" {
			t.Fatalf("got %q", got)
		}
		// A previous model that no longer exists is skipped.
		if got := mustPick(t, p, api.RoleDeveloper, modelChoice{previous: "gone/x", role: rr}, cat); got != "f/a" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("round robin cycles", func(t *testing.T) {
		p := newPicker(nil)
		var got []string
		for range 3 {
			got = append(got, mustPick(t, p, api.RoleDeveloper, modelChoice{role: rr}, cat))
		}
		if strings.Join(got, ",") != "f/a,f/b,f/a" {
			t.Fatalf("got %v", got)
		}
		// Each role has its own cursor.
		if g := mustPick(t, p, api.RoleReviewer, modelChoice{role: role("reviewer", api.AssignmentRoundRobin, "f/a", "f/b")}, cat); g != "f/a" {
			t.Fatalf("reviewer cursor shared with developer: %q", g)
		}
	})
	t.Run("random weights by repeats", func(t *testing.T) {
		r := role("developer", api.AssignmentRandom, "f/a", "f/a", "f/b")
		for n, want := range []string{"f/a", "f/a", "f/b"} {
			p := newPicker(func(k int) int {
				if k != 3 {
					t.Fatalf("rand over %d entries, want 3", k)
				}
				return n
			})
			if got := mustPick(t, p, api.RoleDeveloper, modelChoice{role: r}, cat); got != want {
				t.Fatalf("rand %d picked %q, want %q", n, got, want)
			}
		}
	})
	t.Run("failover by attempt", func(t *testing.T) {
		r := role("developer", api.AssignmentFailover, "gone/x", "f/a", "f/a", "f/b")
		p := newPicker(nil)
		for attempt, want := range []string{"f/a", "f/b", "f/a"} {
			if got := mustPick(t, p, api.RoleDeveloper, modelChoice{role: r, attempt: attempt}, cat); got != want {
				t.Fatalf("attempt %d picked %q, want %q", attempt, got, want)
			}
		}
	})
	t.Run("crash restart in failover mode moves past the previous model", func(t *testing.T) {
		r := role("developer", api.AssignmentFailover, "f/a", "f/a", "f/b", "f/c")
		p := newPicker(nil)
		for _, c := range []struct {
			previous string
			attempt  int
			want     string
		}{
			{"f/a", 1, "f/b"},
			{"f/b", 2, "f/c"},
			{"f/c", 1, "f/a"}, // wraps around
			{"f/b", 0, "f/b"}, // not a crash restart: keep the model
		} {
			if got := mustPick(t, p, api.RoleDeveloper, modelChoice{role: r, previous: c.previous, attempt: c.attempt}, cat); got != c.want {
				t.Fatalf("previous %s attempt %d picked %q, want %q", c.previous, c.attempt, got, c.want)
			}
		}
		// The ticket's own model still wins.
		if got := mustPick(t, p, api.RoleDeveloper, modelChoice{role: r, ticketModel: "f/a", previous: "f/a", attempt: 1}, cat); got != "f/a" {
			t.Fatalf("ticket model lost to failover: %q", got)
		}
		// A previous model the role does not list gives the attempt-th one.
		other := role("developer", api.AssignmentFailover, "f/b", "f/c")
		if got := mustPick(t, p, api.RoleDeveloper, modelChoice{role: other, previous: "f/a", attempt: 1}, cat); got != "f/c" {
			t.Fatalf("unlisted previous gave %q", got)
		}
	})
	t.Run("crash restart in other modes keeps the previous model", func(t *testing.T) {
		p := newPicker(func(int) int { return 0 })
		for _, mode := range []string{api.AssignmentRoundRobin, api.AssignmentRandom, api.AssignmentSmart} {
			r := role("developer", mode, "f/a", "f/b")
			if got := mustPick(t, p, api.RoleDeveloper, modelChoice{role: r, previous: "f/b", attempt: 2}, cat); got != "f/b" {
				t.Fatalf("%s: got %q, want the previous f/b", mode, got)
			}
		}
	})
	t.Run("smart behaves like round robin", func(t *testing.T) {
		p := newPicker(nil)
		r := role("pm", api.AssignmentSmart, "f/a", "f/b")
		var got []string
		for range 3 {
			got = append(got, mustPick(t, p, api.RolePM, modelChoice{role: r}, cat))
		}
		if strings.Join(got, ",") != "f/a,f/b,f/a" {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("missing models skipped", func(t *testing.T) {
		p := newPicker(nil)
		r := role("developer", api.AssignmentRoundRobin, "gone/x", "f/b", "other/y")
		for range 3 {
			if got := mustPick(t, p, api.RoleDeveloper, modelChoice{role: r}, cat); got != "f/b" {
				t.Fatalf("got %q", got)
			}
		}
	})
	t.Run("none usable", func(t *testing.T) {
		for _, r := range []settings.Role{
			role("developer", api.AssignmentRoundRobin),
			role("developer", api.AssignmentRandom, "gone/x"),
			role("developer", "unknown-mode", "gone/x"),
		} {
			p := newPicker(func(int) int { t.Fatal("rand called without models"); return 0 })
			got, err := p.pick(api.RoleDeveloper, modelChoice{role: r}, cat)
			if err == nil || got != "" {
				t.Fatalf("pick = %q, %v; want an error", got, err)
			}
			if want := "no usable model for the developer role; set models in roles/developer.yaml"; err.Error() != want {
				t.Fatalf("error %q, want %q", err, want)
			}
			if hasUsableModel(modelChoice{role: r}, cat) {
				t.Fatal("hasUsableModel true without models")
			}
		}
		if !hasUsableModel(modelChoice{ticketModel: "f/a"}, cat) || !hasUsableModel(modelChoice{previous: "f/b"}, cat) {
			t.Fatal("hasUsableModel ignores the ticket or previous model")
		}
	})
	t.Run("nil catalog", func(t *testing.T) {
		p := newPicker(nil)
		if _, err := p.pick(api.RoleDeveloper, modelChoice{ticketModel: "f/a", role: rr}, nil); err == nil {
			t.Fatal("nil catalog picked a model")
		}
	})
	t.Run("random out of range is clamped", func(t *testing.T) {
		p := newPicker(func(int) int { return 99 })
		r := role("developer", api.AssignmentRandom, "f/a", "f/b")
		if got := mustPick(t, p, api.RoleDeveloper, modelChoice{role: r}, cat); got != "f/b" && got != "f/a" {
			t.Fatalf("got %q", got)
		}
	})
}
