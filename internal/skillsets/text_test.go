package skillsets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

func fakeCheckout(names ...string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		for _, n := range names {
			if n == name {
				return "/home/you/.local/omo/skills/" + name, true
			}
		}
		return "", false
	}
}

func TestTextOnlyEnabledCheckedOutWithText(t *testing.T) {
	sets := []api.SkillSet{
		{Name: "zeta", Enabled: true, Instructions: map[string]string{"developer": "  Use zeta.\n"}},
		{Name: "alpha", Enabled: true, Instructions: map[string]string{"developer": "Read SKILLS.md first.", "reviewer": "Review with alpha."}},
		{Name: "off", Enabled: false, Instructions: map[string]string{"developer": "disabled"}},
		{Name: "nocheckout", Enabled: true, Instructions: map[string]string{"developer": "not fetched"}},
		{Name: "blank", Enabled: true, Instructions: map[string]string{"developer": " \n "}},
	}
	got := Text("developer", sets, fakeCheckout("zeta", "alpha", "off", "blank"))
	want := "Skill set alpha (checked out at /home/you/.local/omo/skills/alpha; read-only, omo updates it):\nRead SKILLS.md first.\n\n" +
		"Skill set zeta (checked out at /home/you/.local/omo/skills/zeta; read-only, omo updates it):\nUse zeta."
	if got != want {
		t.Fatalf("Text =\n%s\nwant\n%s", got, want)
	}
	if got := Text("pm", sets, fakeCheckout("zeta", "alpha")); got != "" {
		t.Fatalf("a role without text got %q", got)
	}
	if got := Text("developer", nil, fakeCheckout()); got != "" {
		t.Fatalf("no sets gave %q", got)
	}
}

func TestTextRemovesHiddenCharacters(t *testing.T) {
	sets := []api.SkillSet{{Name: "s", Enabled: true, Instructions: map[string]string{"developer": "a‮b​c\x1b[31m\r\n\tindented"}}}
	got := Text("developer", sets, fakeCheckout("s"))
	if !strings.HasSuffix(got, "\nabc[31m\n\tindented") {
		t.Fatalf("Text = %q", got)
	}
}

func TestTextIsBounded(t *testing.T) {
	var sets []api.SkillSet
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		sets = append(sets, api.SkillSet{Name: n, Enabled: true, Instructions: map[string]string{"developer": strings.Repeat("x", 16<<10)}})
	}
	got := Text("developer", sets, fakeCheckout("a", "b", "c", "d", "e", "f"))
	if len(got) > MaxText+200 {
		t.Fatalf("Text is %d bytes", len(got))
	}
	if !strings.Contains(got, "Skill set c (") || strings.Contains(got, "Skill set d (") || !strings.HasSuffix(got, "64 KiB: d, e, f.") {
		t.Fatalf("Text ends %q", got[len(got)-200:])
	}
}

// The text is data in the prompt: template actions in it are not run.
func TestTextIsNotATemplate(t *testing.T) {
	sets := []api.SkillSet{{Name: "s", Enabled: true, Instructions: map[string]string{"developer": "{{.Nope}} {{template \"x\"}}"}}}
	d := prompts.SampleData("developer")
	d.Instructions = Join("Be brief.", Text("developer", sets, fakeCheckout("s")))
	out, err := prompts.RenderBounded("developer", prompts.Defaults()["developer"], d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "Be brief.\n\nSkill set s (checked out at /home/you/.local/omo/skills/s; read-only, omo updates it):\n{{.Nope}} {{template \"x\"}}") {
		t.Fatalf("prompt ends:\n%s", out[len(out)-300:])
	}
}

func TestJoin(t *testing.T) {
	for _, c := range []struct{ user, skills, want string }{
		{"", "", ""}, {" u ", "", "u"}, {"", " s ", "s"}, {"u\n", "\ns", "u\n\ns"},
	} {
		if got := Join(c.user, c.skills); got != c.want {
			t.Errorf("Join(%q, %q) = %q, want %q", c.user, c.skills, got, c.want)
		}
	}
}

func TestCheckoutIn(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "s"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "s"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	look := CheckoutIn(dir)
	if p, ok := look("s"); !ok || p != filepath.Join(dir, "s") {
		t.Fatalf("s = %q %v", p, ok)
	}
	for _, name := range []string{"file", "link", "missing", "..", "../s", "S"} {
		if _, ok := look(name); ok {
			t.Errorf("%q counts as a checkout", name)
		}
	}
	if _, ok := CheckoutIn("")("s"); ok {
		t.Error("an empty folder has a checkout")
	}
}
