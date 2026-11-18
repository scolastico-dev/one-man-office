package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/skillsets"
	"github.com/scolastico-dev/one-man-office/internal/testpath"
)

// TestEveryRoleGetsOnlyItsSkillSets: the prompt of every role (CEO and
// export jobs included; the prompt is the same whatever program the agent
// runs) lists only the skill sets given to that role, and of those only
// the skills their paths select.
func TestEveryRoleGetsOnlyItsSkillSets(t *testing.T) {
	h := newHarness(t)
	write := func(rel, text string) {
		t.Helper()
		path := filepath.Join(h.layout.Skills, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("talk/skills/grill-me/SKILL.md", "---\nname: grill-me\n---\n")
	write("talk/skills/other/SKILL.md", "---\nname: talk-other\n---\n")
	write("code/skills/tdd/SKILL.md", "---\nname: tdd\n---\n")
	write("code/skills/debug/SKILL.md", "---\nname: debug\n---\n")
	write("all/SKILL.md", "---\nname: everyone\n---\n")
	// An absolute folder of the platform: "/srv/skills.git" has no drive
	// on Windows and is no absolute path there.
	source := testpath.Abs("/srv/skills.git")
	put := func(k api.SkillSet) {
		t.Helper()
		k.Repository, k.Update, k.Enabled = source, api.SkillSetPinned, true
		if err := h.st.PutSkillSetIf("", k); err != nil {
			t.Fatal(err)
		}
	}
	put(api.SkillSet{Name: "talk", Roles: []string{"ceo"}, Paths: []string{"skills/grill-me"}})
	put(api.SkillSet{Name: "code", Roles: []string{"developer", "reviewer"}, Paths: []string{"skills/tdd"}})
	put(api.SkillSet{Name: "all"})
	for _, name := range []string{"talk", "code", "all"} {
		if err := skillsets.RecordSource(filepath.Join(h.layout.Skills, name), source, ""); err != nil {
			t.Fatal(err)
		}
	}

	task, dev := h.readyDeveloper("one")
	actors := map[string]board.Actor{
		"ceo":       {Role: api.RoleCEO, Name: "Cleo", Session: "s-ceo"},
		"pm":        {Role: api.RolePM, Ticket: task, Name: "Pat", Session: "s-pm"},
		"developer": h.actor(dev.ID),
		"reviewer":  {Role: api.RoleReviewer, Ticket: task, Name: "Rita", Session: "s-rev", ReviewRound: 1},
		"export":    {Role: api.RoleDeveloper, Purpose: api.PurposeExport, Ticket: task, Name: "Eve", Session: "s-exp"},
	}
	want := map[string][]string{
		"ceo":       {"- grill-me", "- everyone"},
		"pm":        {"- everyone"},
		"developer": {"- tdd", "- everyone"},
		"reviewer":  {"- tdd", "- everyone"},
		"export":    {"- everyone"},
	}
	all := []string{"- grill-me", "- talk-other", "- tdd", "- debug", "- everyone"}
	for role, a := range actors {
		p, err := h.e.rolePrompt(h.ctx, a)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		for _, skill := range all {
			has := strings.Contains(p, skill+"\n")
			wanted := false
			for _, w := range want[role] {
				wanted = wanted || w == skill
			}
			if has != wanted {
				t.Errorf("%s: has %s = %v, want %v", role, skill, has, wanted)
			}
		}
	}
}
