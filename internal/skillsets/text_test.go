package skillsets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

// skillsDir is the skills folder of fakeCheckout.
var skillsDir string

// fakeCheckout makes checkouts of names (fetched from no repository, as
// the sets of these tests name none) in a new skills folder, skillsDir.
func fakeCheckout(t *testing.T, names ...string) func(string) (string, bool) {
	t.Helper()
	skillsDir = t.TempDir()
	for _, n := range names {
		if err := RecordSource(filepath.Join(skillsDir, n), "", ""); err != nil {
			t.Fatal(err)
		}
	}
	return CheckoutIn(skillsDir)
}

func TestTextOnlyEnabledCheckedOutWithText(t *testing.T) {
	sets := []api.SkillSet{
		{Name: "zeta", Enabled: true, Instructions: map[string]string{"developer": "  Use zeta.\n"}},
		{Name: "alpha", Enabled: true, Instructions: map[string]string{"developer": "Read SKILLS.md first.", "reviewer": "Review with alpha."}},
		{Name: "off", Enabled: false, Instructions: map[string]string{"developer": "disabled"}},
		{Name: "nocheckout", Enabled: true, Instructions: map[string]string{"developer": "not fetched"}},
		{Name: "blank", Enabled: true, Instructions: map[string]string{"developer": " \n "}},
	}
	got := Text("developer", sets, fakeCheckout(t, "zeta", "alpha", "off", "blank"))
	want := "Skill set alpha (checked out at " + filepath.Join(skillsDir, "alpha") + "; read-only, omo updates it):\nRead SKILLS.md first.\n\n" +
		"Skill set zeta (checked out at " + filepath.Join(skillsDir, "zeta") + "; read-only, omo updates it):\nUse zeta."
	if got != want {
		t.Fatalf("Text =\n%s\nwant\n%s", got, want)
	}
	if got := Text("pm", sets, fakeCheckout(t, "zeta", "alpha")); got != "" {
		t.Fatalf("a role without text got %q", got)
	}
	if got := Text("developer", nil, fakeCheckout(t)); got != "" {
		t.Fatalf("no sets gave %q", got)
	}
}

func TestTextRemovesHiddenCharacters(t *testing.T) {
	sets := []api.SkillSet{{Name: "s", Enabled: true, Instructions: map[string]string{"developer": "a‮b​c\x1b[31m\r\n\tindented"}}}
	got := Text("developer", sets, fakeCheckout(t, "s"))
	if !strings.HasSuffix(got, "\nabc[31m\n\tindented") {
		t.Fatalf("Text = %q", got)
	}
}

func TestTextIsBounded(t *testing.T) {
	var sets []api.SkillSet
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		sets = append(sets, api.SkillSet{Name: n, Enabled: true, Instructions: map[string]string{"developer": strings.Repeat("x", 16<<10)}})
	}
	got := Text("developer", sets, fakeCheckout(t, "a", "b", "c", "d", "e", "f"))
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
	d.Instructions = Join("Be brief.", Text("developer", sets, fakeCheckout(t, "s")))
	out, err := prompts.RenderBounded("developer", prompts.Defaults()["developer"], d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "Be brief.\n\nSkill set s (checked out at "+filepath.Join(skillsDir, "s")+"; read-only, omo updates it):\n{{.Nope}} {{template \"x\"}}") {
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

// skillTree writes a checkout with skills in several shapes and answers
// its folder.
func skillTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "s")
	files := map[string]string{
		"README.md":                         "# Skills\n",
		"skills/tdd/SKILL.md":               "---\nname: tdd\ndescription: Write the test first.\n---\nBody.\n",
		"skills/tdd/notes.md":               "not a skill\n",
		"skills/brainstorm/SKILL.md":        "# Brainstorm\nNo front matter.\n",
		"skills/nested/group/deep/SKILL.md": "---\ndescription: |\n  Deep\n  down.\n---\n",
		"skills/nested/group/also/SKILL.md": "---\nname: also\n---\n",
		"skills/manual/SKILL.md":            "---\nname: manual\ndescription: Only by hand.\ndisable-model-invocation: true\n---\n",
		"docs/style.md":                     "---\ndescription: How we write.\n---\nText.\n",
		"docs/plain.txt":                    "plain\n",
		".hidden/x/SKILL.md":                "---\nname: hidden\n---\n",
	}
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// at is the checkout lookup of the set s at root, fetched from no
// repository as the sets of these tests name none.
func at(root string) func(string) (string, bool) {
	if err := RecordSource(root, "", ""); err != nil {
		panic(err)
	}
	return func(name string) (string, bool) { return root, name == "s" }
}

const wantIntro = "Its skills: when a task fits a skill's description, or someone names the skill, read the skill's file and follow it."

// The whole repository: every folder with a SKILL.md is one skill (hidden
// folders are left out); other files are not skills.
func TestTextListsTheSkillsOfTheWholeRepository(t *testing.T) {
	root := skillTree(t)
	sets := []api.SkillSet{{Name: "s", Enabled: true}}
	got := Text("ceo", sets, at(root))
	f := func(p string) string { return filepath.Join(root, filepath.FromSlash(p)) }
	want := "Skill set s (checked out at " + root + "; read-only, omo updates it):\n" + wantIntro + "\n" +
		"- brainstorm\n  " + f("skills/brainstorm/SKILL.md") + "\n" +
		"- manual (only when someone asks for it): Only by hand.\n  " + f("skills/manual/SKILL.md") + "\n" +
		"- also\n  " + f("skills/nested/group/also/SKILL.md") + "\n" +
		"- deep: Deep down.\n  " + f("skills/nested/group/deep/SKILL.md") + "\n" +
		"- tdd: Write the test first.\n  " + f("skills/tdd/SKILL.md")
	if got != want {
		t.Fatalf("Text =\n%s\nwant\n%s", got, want)
	}
}

// Selected paths: a folder with a SKILL.md is that skill, a folder without
// one gives every skill below it, a SKILL.md file is the skill of its
// folder, any other file is a skill of its own named after the file. A
// path the checkout does not have is left out; the role's text comes
// first.
func TestTextHonoursPaths(t *testing.T) {
	root := skillTree(t)
	sets := []api.SkillSet{{
		Name: "s", Enabled: true,
		Paths:        []string{"skills/tdd", "docs/style.md", "skills/nested", "skills/brainstorm/SKILL.md", "docs/plain.txt", "gone", "skills/tdd/SKILL.md"},
		Instructions: map[string]string{"developer": "Use them."},
	}}
	got := Text("developer", sets, at(root))
	f := func(p string) string { return filepath.Join(root, filepath.FromSlash(p)) }
	want := "Skill set s (checked out at " + root + "; read-only, omo updates it):\nUse them.\n" + wantIntro + "\n" +
		"- tdd: Write the test first.\n  " + f("skills/tdd/SKILL.md") + "\n" +
		"- style: How we write.\n  " + f("docs/style.md") + "\n" +
		"- also\n  " + f("skills/nested/group/also/SKILL.md") + "\n" +
		"- deep: Deep down.\n  " + f("skills/nested/group/deep/SKILL.md") + "\n" +
		"- brainstorm\n  " + f("skills/brainstorm/SKILL.md") + "\n" +
		"- plain\n  " + f("docs/plain.txt")
	if got != want {
		t.Fatalf("Text =\n%s\nwant\n%s", got, want)
	}
	if got := Missing(root, sets[0].Paths); len(got) != 1 || got[0] != "gone" {
		t.Fatalf("Missing = %q", got)
	}
	if got := Missing(filepath.Join(root, "nope"), sets[0].Paths); len(got) != 0 {
		t.Fatalf("Missing without checkout = %q", got)
	}
}

// Roles: a set with roles reaches only those roles; one without reaches
// every role. A set with neither text nor skills for a role says nothing.
func TestTextHonoursRoles(t *testing.T) {
	root := skillTree(t)
	only := []api.SkillSet{{Name: "s", Enabled: true, Roles: []string{"ceo"}, Paths: []string{"skills/manual"}}}
	for _, role := range []string{"pm", "developer", "reviewer", "export"} {
		if got := Text(role, only, at(root)); got != "" {
			t.Errorf("%s got %q", role, got)
		}
	}
	if got := Text("ceo", only, at(root)); !strings.Contains(got, "- manual (only when someone asks for it)") {
		t.Errorf("ceo got %q", got)
	}
	all := []api.SkillSet{{Name: "s", Enabled: true, Paths: []string{"skills/manual"}}}
	for _, role := range []string{"ceo", "pm", "developer", "reviewer", "export"} {
		if got := Text(role, all, at(root)); !strings.Contains(got, "- manual") {
			t.Errorf("%s got %q", role, got)
		}
	}
	empty := []api.SkillSet{{Name: "s", Enabled: true, Paths: []string{"README.md/x"}}}
	if got := Text("ceo", empty, at(root)); got != "" {
		t.Errorf("nothing to say gave %q", got)
	}
	off := []api.SkillSet{{Name: "s", Enabled: false}}
	if got := Text("ceo", off, at(root)); got != "" {
		t.Errorf("a disabled set gave %q", got)
	}
}

// A link in a checkout is never followed, neither as a selected path nor
// while looking for skills.
func TestTextFollowsNoLinks(t *testing.T) {
	root := skillTree(t)
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "evil", "SKILL.md"), "---\nname: evil\n---\n")
	if err := os.Symlink(filepath.Join(outside, "evil"), filepath.Join(root, "skills", "evil")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "evil", "SKILL.md"), filepath.Join(root, "docs", "evil.md")); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{nil, {"skills/evil"}, {"docs/evil.md"}, {"skills"}} {
		got := Text("ceo", []api.SkillSet{{Name: "s", Enabled: true, Paths: paths}}, at(root))
		if strings.Contains(got, "evil") {
			t.Errorf("%q: %s", paths, got)
		}
	}
	if got := Missing(root, []string{"skills/evil", "docs/evil.md"}); len(got) != 2 {
		t.Errorf("Missing = %q", got)
	}
}

// Descriptions are one line without hidden characters, cut at 300
// characters; a name that is not a plain short name is replaced by the
// folder's.
func TestTextCleansWhatTheCheckoutSays(t *testing.T) {
	root := filepath.Join(t.TempDir(), "s")
	writeTestFile(t, filepath.Join(root, "a", "SKILL.md"), "---\nname: \"bad\\u202ename\\nsecond line\"\ndescription: \"x\\u200by "+strings.Repeat("z", 400)+"\"\n---\n")
	got := Text("ceo", []api.SkillSet{{Name: "s", Enabled: true}}, at(root))
	line := strings.Split(got, "\n")[2]
	if !strings.HasPrefix(line, "- a: xy zzz") || !strings.HasSuffix(line, "…") || len([]rune(line)) > 310 {
		t.Fatalf("line = %q", line)
	}
}

func writeTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A checkout is the set's only while it was fetched from the set's
// repository and ref: after a change of either it gives agents nothing.
func TestTextLeavesOutACheckoutOfAnotherSource(t *testing.T) {
	root := skillTree(t)
	if err := RecordSource(root, "https://example.invalid/a.git", "main"); err != nil {
		t.Fatal(err)
	}
	look := func(string) (string, bool) { return root, true }
	set := api.SkillSet{Name: "s", Enabled: true, Repository: "https://example.invalid/a.git", Ref: "main"}
	if got := Text("ceo", []api.SkillSet{set}, look); !strings.Contains(got, "- tdd") {
		t.Fatalf("the set's own checkout gave %q", got)
	}
	for _, k := range []api.SkillSet{
		{Name: "s", Enabled: true, Repository: "https://example.invalid/b.git", Ref: "main"},
		{Name: "s", Enabled: true, Repository: "https://example.invalid/a.git", Ref: "v2"},
	} {
		if got := Text("ceo", []api.SkillSet{k}, look); got != "" {
			t.Errorf("%s at %s got the old checkout: %q", k.Repository, k.Ref, got)
		}
	}
	if err := os.Remove(filepath.Join(root, ".git", sourceFile)); err != nil {
		t.Fatal(err)
	}
	if got := Text("ceo", []api.SkillSet{set}, look); got != "" {
		t.Errorf("a checkout without its source got %q", got)
	}
}
