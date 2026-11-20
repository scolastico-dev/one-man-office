package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

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
	return mustPickUsing(t, p, r, c, cat, nil)
}

func mustPickUsing(t *testing.T, p *picker, r api.Role, c modelChoice, cat modelCatalog, use usageFunc) string {
	t.Helper()
	got, err := p.pick(r, c, cat, use)
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
		for _, mode := range []string{api.AssignmentRoundRobin, api.AssignmentRandom, api.AssignmentSmart, api.AssignmentSuperSmart} {
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
			got, err := p.pick(api.RoleDeveloper, modelChoice{role: r}, cat, nil)
			if err == nil || got != "" {
				t.Fatalf("pick = %q, %v; want an error", got, err)
			}
			if want := "no usable model for the developer role; set models in roles/developer.yaml"; err.Error() != want {
				t.Fatalf("error %q, want %q", err, want)
			}
			if canStart(modelChoice{role: r}, cat, nil) != startNoModel {
				t.Fatal("canStart without models is not startNoModel")
			}
		}
		if canStart(modelChoice{ticketModel: "f/a"}, cat, nil) != startOK || canStart(modelChoice{previous: "f/b"}, cat, nil) != startOK {
			t.Fatal("canStart ignores the ticket or previous model")
		}
	})
	t.Run("nil catalog", func(t *testing.T) {
		p := newPicker(nil)
		if _, err := p.pick(api.RoleDeveloper, modelChoice{ticketModel: "f/a", role: rr}, nil, nil); err == nil {
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

// usageOf builds a usage function: each entry is a state, optionally with a
// percentage ("ok:60"); missing models are unmetered.
func usageOf(states map[string]string) usageFunc {
	return func(id string) api.ModelUsage {
		v, ok := states[id]
		if !ok {
			return api.ModelUsage{Model: id, State: api.UsageUnmetered}
		}
		state, pct, hasPct := strings.Cut(v, ":")
		u := api.ModelUsage{Model: id, State: state}
		if hasPct {
			var f float64
			fmt.Sscan(pct, &f)
			u.UsedPercent = &f
			u.Window = "session"
		}
		return u
	}
}

func picks(t *testing.T, p *picker, r api.Role, c modelChoice, cat modelCatalog, use usageFunc, n int) string {
	t.Helper()
	var got []string
	for range n {
		got = append(got, mustPickUsing(t, p, r, c, cat, use))
	}
	return strings.Join(got, ",")
}

func TestPickSkipsModelsOverSoftUsage(t *testing.T) {
	cat := fakeCatalog{"f/a": true, "f/b": true, "f/c": true}
	use := usageOf(map[string]string{"f/a": api.UsageSoft + ":91", "f/b": api.UsageOK + ":10", "f/c": api.UsageHard + ":99"})
	for _, mode := range []string{api.AssignmentRoundRobin, api.AssignmentRandom, api.AssignmentFailover, api.AssignmentSmart, api.AssignmentSuperSmart} {
		t.Run(mode, func(t *testing.T) {
			p := newPicker(func(n int) int { return n - 1 })
			r := role("developer", mode, "f/a", "f/c", "f/b", "f/a")
			for attempt := range 3 {
				if got := mustPickUsing(t, p, api.RoleDeveloper, modelChoice{role: r, attempt: attempt}, cat, use); got != "f/b" {
					t.Fatalf("attempt %d picked %q, want the only eligible f/b", attempt, got)
				}
			}
			if canStart(modelChoice{role: r}, cat, use) != startOK {
				t.Fatal("canStart does not see the eligible model")
			}
			// Nothing eligible: wait for usage, not "no model".
			over := role("developer", mode, "f/a", "f/c", "gone/x")
			if _, err := p.pick(api.RoleDeveloper, modelChoice{role: over}, cat, use); !errors.Is(err, errUsageWait) {
				t.Fatalf("pick = %v, want errUsageWait", err)
			}
			if canStart(modelChoice{role: over}, cat, use) != startUsage {
				t.Fatal("canStart is not startUsage")
			}
		})
	}
	t.Run("random keeps weights among eligible entries", func(t *testing.T) {
		r := role("developer", api.AssignmentRandom, "f/a", "f/b", "f/b", "f/c", "f/d")
		cat := fakeCatalog{"f/a": true, "f/b": true, "f/c": true, "f/d": true}
		for n, want := range []string{"f/b", "f/b", "f/d"} {
			p := newPicker(func(k int) int {
				if k != 3 {
					t.Fatalf("rand over %d entries, want the 3 eligible ones", k)
				}
				return n
			})
			if got := mustPickUsing(t, p, api.RoleDeveloper, modelChoice{role: r}, cat, use); got != want {
				t.Fatalf("rand %d picked %q, want %q", n, got, want)
			}
		}
	})
	t.Run("round robin moves its cursor past ineligible models", func(t *testing.T) {
		cat := fakeCatalog{"f/a": true, "f/b": true, "f/c": true, "f/d": true}
		r := role("developer", api.AssignmentRoundRobin, "f/a", "f/b", "f/c", "f/d")
		if got := picks(t, newPicker(nil), api.RoleDeveloper, modelChoice{role: r}, cat, use, 4); got != "f/b,f/d,f/b,f/d" {
			t.Fatalf("got %s", got)
		}
	})
	t.Run("failover keeps its order and moves on over ineligible models", func(t *testing.T) {
		cat := fakeCatalog{"f/a": true, "f/b": true, "f/c": true, "f/d": true}
		r := role("developer", api.AssignmentFailover, "f/d", "f/a", "f/b", "f/c")
		p := newPicker(nil)
		for attempt, want := range []string{"f/d", "f/b", "f/b", "f/d", "f/d"} {
			if got := mustPickUsing(t, p, api.RoleDeveloper, modelChoice{role: r, attempt: attempt}, cat, use); got != want {
				t.Fatalf("attempt %d picked %q, want %q", attempt, got, want)
			}
		}
		// A crash restart moves past the previous model to the next eligible.
		if got := mustPickUsing(t, p, api.RoleDeveloper, modelChoice{role: r, previous: "f/d", attempt: 1}, cat, use); got != "f/b" {
			t.Fatalf("crash restart after f/d picked %q", got)
		}
	})
	t.Run("unknown and unmetered models are eligible", func(t *testing.T) {
		use := usageOf(map[string]string{"f/a": api.UsageUnknown})
		r := role("developer", api.AssignmentRoundRobin, "f/a", "f/b")
		if got := picks(t, newPicker(nil), api.RoleDeveloper, modelChoice{role: r}, cat, use, 2); got != "f/a,f/b" {
			t.Fatalf("got %s", got)
		}
	})
}

func TestPickPreviousModelDroppedOverSoft(t *testing.T) {
	cat := fakeCatalog{"f/a": true, "f/b": true}
	r := role("developer", api.AssignmentRoundRobin, "f/a", "f/b")
	p := newPicker(nil)
	use := usageOf(map[string]string{"f/b": api.UsageSoft})
	if got := mustPickUsing(t, p, api.RoleDeveloper, modelChoice{role: r, previous: "f/b"}, cat, use); got != "f/a" {
		t.Fatalf("got %q, want f/a instead of the previous f/b", got)
	}
	// Below its limit the previous model is kept.
	if got := mustPickUsing(t, p, api.RoleDeveloper, modelChoice{role: r, previous: "f/b"}, cat, usageOf(nil)); got != "f/b" {
		t.Fatalf("got %q", got)
	}
	// A previous model over soft with no role models left: wait for usage.
	none := role("developer", api.AssignmentRoundRobin)
	if _, err := p.pick(api.RoleDeveloper, modelChoice{role: none, previous: "f/b"}, cat, use); !errors.Is(err, errUsageWait) {
		t.Fatalf("pick = %v, want errUsageWait", err)
	}
	if canStart(modelChoice{role: none, previous: "f/b"}, cat, use) != startUsage {
		t.Fatal("canStart is not startUsage")
	}
}

func TestPickTicketModelOverSoftWaits(t *testing.T) {
	cat := fakeCatalog{"f/a": true, "f/b": true}
	r := role("developer", api.AssignmentRoundRobin, "f/a", "f/b")
	for _, state := range []string{api.UsageSoft, api.UsageHard} {
		use := usageOf(map[string]string{"f/a": state})
		p := newPicker(nil)
		// Other models are eligible, but the ticket's own model decides.
		c := modelChoice{role: r, ticketModel: "f/a"}
		if _, err := p.pick(api.RoleDeveloper, c, cat, use); !errors.Is(err, errUsageWait) {
			t.Fatalf("%s: pick = %v, want errUsageWait", state, err)
		}
		if canStart(c, cat, use) != startUsage {
			t.Fatalf("%s: canStart is not startUsage", state)
		}
		c.forced = true
		if got := mustPickUsing(t, p, api.RoleDeveloper, c, cat, use); got != "f/a" {
			t.Fatalf("%s: forced pick %q", state, got)
		}
		if canStart(c, cat, use) != startOK {
			t.Fatalf("%s: forced canStart is not startOK", state)
		}
	}
}

func TestSmartPicksMostCapacity(t *testing.T) {
	cat := fakeCatalog{"f/a": true, "f/b": true, "f/c": true}
	r := role("developer", api.AssignmentSmart, "f/a", "f/b", "f/a")
	use := usageOf(map[string]string{"f/a": "ok:60", "f/b": "ok:20"})
	if got := picks(t, newPicker(nil), api.RoleDeveloper, modelChoice{role: r}, cat, use, 3); got != "f/b,f/b,f/b" {
		t.Fatalf("got %s, want the emptier f/b every time (repeats of f/a do not count)", got)
	}
	tie := usageOf(map[string]string{"f/a": "ok:20", "f/b": "ok:20"})
	if got := picks(t, newPicker(nil), api.RoleDeveloper, modelChoice{role: role("developer", api.AssignmentSmart, "f/b", "f/a")}, cat, tie, 2); got != "f/b,f/b" {
		t.Fatalf("tie: got %s, want the first listed f/b", got)
	}
	// A model over its soft threshold is never chosen, however it compares.
	soft := usageOf(map[string]string{"f/a": "soft:91", "f/b": "ok:95", "f/c": "ok:96"})
	if got := mustPickUsing(t, newPicker(nil), api.RoleDeveloper, modelChoice{role: role("developer", api.AssignmentSmart, "f/a", "f/b", "f/c")}, cat, soft); got != "f/b" {
		t.Fatalf("got %s", got)
	}
}

func TestSmartUnmeteredTakesTurns(t *testing.T) {
	for _, mode := range []string{api.AssignmentSmart, api.AssignmentSuperSmart} {
		t.Run(mode, func(t *testing.T) {
			cat := fakeCatalog{"f/a": true, "f/u": true, "f/k": true, "f/b": true}
			use := usageOf(map[string]string{"f/a": "ok:20", "f/b": "ok:50", "f/k": api.UsageUnknown})
			r := role("developer", mode, "f/a", "f/u")
			if got := picks(t, newPicker(nil), api.RoleDeveloper, modelChoice{role: r}, cat, use, 4); got != "f/a,f/u,f/a,f/u" {
				t.Fatalf("got %s", got)
			}
			// Unknown models take turns too; the fuller metered f/b does not.
			r = role("developer", mode, "f/b", "f/k", "f/a")
			if got := picks(t, newPicker(nil), api.RoleDeveloper, modelChoice{role: r}, cat, use, 4); got != "f/k,f/a,f/k,f/a" {
				t.Fatalf("got %s", got)
			}
		})
	}
}

func TestSmartWithoutReadingsIsRoundRobin(t *testing.T) {
	for _, mode := range []string{api.AssignmentSmart, api.AssignmentSuperSmart} {
		t.Run(mode, func(t *testing.T) {
			cat := fakeCatalog{"f/a": true, "f/b": true, "f/c": true}
			use := usageOf(map[string]string{"f/a": api.UsageUnknown})
			r := role("developer", mode, "f/a", "f/b", "f/c")
			for _, u := range []usageFunc{nil, use} {
				if got := picks(t, newPicker(nil), api.RoleDeveloper, modelChoice{role: r}, cat, u, 4); got != "f/a,f/b,f/c,f/a" {
					t.Fatalf("got %s", got)
				}
			}
		})
	}
}

func TestSuperSmartPrefersExpiringCapacityAsResetApproaches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		resetA, resetB := now.Add(24*time.Hour), now.Add(7*24*time.Hour)
		usedA, usedB := 80.0, 20.0
		use := usageFunc(func(id string) api.ModelUsage {
			if id == "f/a" {
				return api.ModelUsage{State: api.UsageOK, UsedPercent: &usedA, ResetsAt: &resetA}
			}
			return api.ModelUsage{State: api.UsageOK, UsedPercent: &usedB, ResetsAt: &resetB}
		})
		cat := fakeCatalog{"f/a": true, "f/b": true}
		c := modelChoice{role: role("developer", api.AssignmentSuperSmart, "f/a", "f/b", "f/a")}
		p := newPicker(nil)
		if got := picks(t, p, api.RoleDeveloper, c, cat, use, 3); got != "f/b,f/b,f/b" {
			t.Fatalf("24h left: %s", got)
		}
		time.Sleep(20 * time.Hour) // Virtual time: the same readings now have 4h and 148h left.
		if got := picks(t, p, api.RoleDeveloper, c, cat, use, 3); got != "f/a,f/a,f/a" {
			t.Fatalf("4h left: %s", got)
		}
		c.role.Assignment = api.AssignmentSmart
		if got := mustPickUsing(t, p, api.RoleDeveloper, c, cat, use); got != "f/b" {
			t.Fatalf("smart must still prefer capacity: %s", got)
		}
	})
}

func TestSuperSmartResetFallbackAndTies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		future, past, zero := now.Add(time.Hour), now.Add(-time.Hour), time.Time{}
		for _, tc := range []struct {
			name           string
			resetA, resetB *time.Time
			usedA, usedB   float64
			want           string
		}{
			{"no resets", nil, nil, 80, 20, "f/b"},
			{"equal resets", &future, &future, 80, 20, "f/b"},
			{"equal scores keep list order", &future, &future, 20, 20, "f/a"},
			{"past reset has no bonus", &past, nil, 80, 20, "f/b"},
			{"reset now has no bonus", &now, nil, 80, 20, "f/b"},
			{"zero reset has no bonus", &zero, nil, 80, 20, "f/b"},
			{"future reset gets bonus", &future, nil, 80, 20, "f/a"},
		} {
			use := usageFunc(func(id string) api.ModelUsage {
				if id == "f/a" {
					return api.ModelUsage{State: api.UsageOK, UsedPercent: &tc.usedA, ResetsAt: tc.resetA}
				}
				return api.ModelUsage{State: api.UsageOK, UsedPercent: &tc.usedB, ResetsAt: tc.resetB}
			})
			c := modelChoice{role: role("developer", api.AssignmentSuperSmart, "f/a", "f/b")}
			if got := picks(t, newPicker(nil), api.RoleDeveloper, c, fakeCatalog{"f/a": true, "f/b": true}, use, 2); got != tc.want+","+tc.want {
				t.Errorf("%s: got %s, want %s twice", tc.name, got, tc.want)
			}
		}
	})
}
