package prompts

import (
	"strings"
	"testing"
)

// TestCEOPromptSaysItsTicketsWaitForTheUser: while the CEO's tickets wait
// for the user's approval, the CEO's prompt says so and tells it to tell
// the user; without the setting the sentence is absent.
func TestCEOPromptSaysItsTicketsWaitForTheUser(t *testing.T) {
	const sentence = "every story and task you create waits in Planning until the user approves it"
	for _, need := range []bool{true, false} {
		d := Data{Name: "Cleo", Role: "ceo", TaskRelease: "agents", CEOMayApprove: true, CEOTicketsNeedApproval: need}
		out, err := Render("ceo", Defaults()["ceo"], d)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, sentence) != need {
			t.Errorf("setting %v: sentence shown %v:\n%s", need, !need, out)
		}
		if need && !strings.Contains(out, "tell the user that it waits for their approval") {
			t.Errorf("the CEO is not told to tell the user:\n%s", out)
		}
		if strings.Contains(out, "\n\n\n") || strings.Contains(out, "{{") {
			t.Errorf("setting %v: broken layout:\n%s", need, out)
		}
	}
}

// TestCEOPromptReleasesStandaloneTasksOnlyWhereThatApplies: the line that
// lets the CEO release a standalone task it created is absent while its
// tickets wait for the user's approval, as it could never apply then.
func TestCEOPromptReleasesStandaloneTasksOnlyWhereThatApplies(t *testing.T) {
	const release = "You may release a standalone task you created"
	for _, c := range []struct {
		release string
		need    bool
		want    bool
	}{{"agents", false, true}, {"agents", true, false}, {"user", false, false}, {"user", true, false}} {
		d := Data{Name: "Cleo", Role: "ceo", TaskRelease: c.release, CEOMayApprove: true, CEOTicketsNeedApproval: c.need}
		out, err := Render("ceo", Defaults()["ceo"], d)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, release) != c.want {
			t.Errorf("release %s, approval switch %v: line shown %v", c.release, c.need, !c.want)
		}
		if strings.Contains(out, "\n\n\n") || strings.Contains(out, "{{") {
			t.Errorf("broken layout:\n%s", out)
		}
	}
}
