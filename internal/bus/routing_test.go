package bus

import "testing"

// fakeDir is an in-memory Directory for matrix tests.
type fakeDir struct {
	roles    map[string]string
	pmOf     map[string]string
	devsOf   map[string][]string
	revDev   map[string]string
	revPM    map[string]string
	contacts map[string]map[string]bool
}

func (f fakeDir) Role(a string) (string, bool) { r, ok := f.roles[a]; return r, ok }
func (f fakeDir) LivingAgents() []string {
	var out []string
	for a := range f.roles {
		out = append(out, a)
	}
	return out
}
func (f fakeDir) AgentsByRole(role string) []string {
	var out []string
	for a, r := range f.roles {
		if r == role {
			out = append(out, a)
		}
	}
	return out
}
func (f fakeDir) CEO() (string, bool) {
	for a, r := range f.roles {
		if r == "ceo" {
			return a, true
		}
	}
	return "", false
}
func (f fakeDir) PMOf(dev string) (string, bool)  { p, ok := f.pmOf[dev]; return p, ok }
func (f fakeDir) DevelopersOf(pm string) []string { return f.devsOf[pm] }
func (f fakeDir) ReviewTargets(rev string) (string, string, bool) {
	d, ok := f.revDev[rev]
	return d, f.revPM[rev], ok
}
func (f fakeDir) ReviewerOf(dev string) (string, bool) {
	for reviewer, target := range f.revDev {
		if target == dev {
			return reviewer, true
		}
	}
	return "", false
}
func (f fakeDir) FirefighterContacted(firefighter, agent string) bool {
	return f.contacts[firefighter][agent]
}

func dir() fakeDir {
	return fakeDir{
		roles: map[string]string{
			"ceo-ada": "ceo", "pm-alex": "product_manager", "pm-nina": "product_manager",
			"developer-jason": "developer", "developer-mia": "developer",
			"reviewer-sara": "reviewer", "freelancer-tom": "freelancer",
			"smokealarm-leo": "smokealarm", "firefighter-max": "firefighter",
		},
		pmOf:     map[string]string{"developer-jason": "pm-alex", "developer-mia": "pm-nina"},
		devsOf:   map[string][]string{"pm-alex": {"developer-jason"}, "pm-nina": {"developer-mia"}},
		revDev:   map[string]string{"reviewer-sara": "developer-jason"},
		revPM:    map[string]string{"reviewer-sara": "pm-alex"},
		contacts: map[string]map[string]bool{"firefighter-max": {"developer-jason": true}},
	}
}

func TestRoutingMatrix(t *testing.T) {
	d := dir()
	allow := []struct{ from, to string }{
		{"developer-jason", "pm-alex"},         // developer → own PM
		{"developer-jason", "reviewer-sara"},   // developer → current reviewer
		{"developer-jason", "ceo-ada"},         // emergency channel
		{"smokealarm-leo", "ceo"},              // emergency channel via role
		{"pm-alex", "ceo-ada"},                 // PM → CEO
		{"pm-alex", "developer-jason"},         // PM → own developer
		{"pm-alex", "pm-nina"},                 // PM → other PM (lateral)
		{"freelancer-tom", "ceo-ada"},          // freelancer → CEO
		{"reviewer-sara", "developer-jason"},   // reviewer → reviewed dev
		{"reviewer-sara", "pm-alex"},           // reviewer → job's PM
		{"ceo-ada", "user"},                    // CEO → anyone
		{"ceo-ada", ""},                        // CEO broadcast
		{"firefighter-max", ""},                // firefighter broadcast
		{"firefighter-max", "developer-mia"},   // firefighter → anyone
		{"developer-jason", "firefighter-max"}, // reply to contacting firefighter
		{SystemSender, "developer-mia"},        // supervisor-generated message
		{"user", "pm-nina"},                    // user is a normal participant
	}
	for _, c := range allow {
		if err := Allowed(d, c.from, c.to); err != nil {
			t.Errorf("expected allow %s→%q, got %v", c.from, c.to, err)
		}
	}
	deny := []struct{ from, to string }{
		{"developer-jason", "pm-nina"},        // not own PM
		{"developer-jason", "developer-mia"},  // no dev↔dev
		{"developer-mia", "firefighter-max"},  // firefighter did not contact this agent
		{"developer-jason", "user"},           // no dev → user
		{"developer-jason", ""},               // no dev broadcast
		{"pm-alex", "developer-mia"},          // not own developer
		{"pm-alex", "user"},                   // PM may not message user
		{"freelancer-tom", "pm-alex"},         // freelancer only CEO
		{"reviewer-sara", "developer-mia"},    // not the reviewed dev
		{"smokealarm-leo", "developer-jason"}, // smoke alarm: incidents only
		{"smokealarm-leo", ""},                // no broadcast
	}
	for _, c := range deny {
		if err := Allowed(d, c.from, c.to); err == nil {
			t.Errorf("expected deny %s→%q", c.from, c.to)
		}
	}
}
