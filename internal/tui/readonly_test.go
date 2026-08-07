package tui

import "testing"

// Typing into a worker agent's session by accident derails it, so peeks open
// read-only everywhere except the CEO — whose session is the whole point of
// the home screen. Ctrl+T still toggles either way.
func TestDefaultReadOnlyByAgent(t *testing.T) {
	cases := []struct {
		agent, ceo string
		want       bool
	}{
		{"ceo-ada", "ceo-ada", false},        // the CEO is who you talk to
		{"developer-jason", "ceo-ada", true}, // everyone else is observed
		{"reviewer-sara", "ceo-ada", true},
		{"pm-alex", "ceo-ada", true},
		{"ceo-ada", "", true}, // no known CEO: stay safe
	}
	for _, c := range cases {
		if got := defaultReadOnly(c.agent, c.ceo); got != c.want {
			t.Errorf("defaultReadOnly(%q, ceo=%q) = %v, want %v", c.agent, c.ceo, got, c.want)
		}
	}
}
