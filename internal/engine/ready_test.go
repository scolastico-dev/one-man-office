package engine

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/skillsets"
	"github.com/scolastico-dev/one-man-office/internal/testpath"
)

func TestReadyRendersRestartNoteForFreshSession(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	first := h.live(api.RoleDeveloper, task)
	p, err := h.e.Ready(h.ctx, h.actor(first.ID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p, "run `git status`") {
		t.Fatalf("the first session got the restart note:\n%s", p)
	}
	wt := WorktreeDir(h.layout.Worktrees, h.project, task)
	for _, want := range []string{"You are " + first.Name + ", the developer of task #", "your worktree is " + wt, "cut from main",
		"Your supervisor is the user."} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, first.ID) {
		t.Fatal("the prompt contains the session id")
	}
	h.agent(first.ID).exit(nil)
	h.waitEnded(first.ID)
	h.step()
	second := h.live(api.RoleDeveloper, task)
	p, err = h.e.Ready(h.ctx, h.actor(second.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "run `git status`") {
		t.Fatalf("the fresh session lacks the restart note:\n%s", p)
	}
}

func TestReadyForTaskOfStoryNamesIntegrationBranch(t *testing.T) {
	h := newHarness(t)
	s := h.story("Story")
	h.approve(s)
	h.step()
	pm := h.live(api.RolePM, s)
	p, err := h.e.Ready(h.ctx, h.actor(pm.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "the product manager of story #") || !strings.Contains(p, "cut from main") {
		t.Fatalf("pm prompt:\n%s", p)
	}
	task := h.taskAs(h.actor(pm.ID), s, "task")
	h.approveAs(h.actor(pm.ID), task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	p, err = h.e.Ready(h.ctx, h.actor(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	integration := h.ticket(s).Projects[0].Branch
	for _, want := range []string{"cut from " + integration, "Your supervisor is the product manager.", "belongs to story #"} {
		if !strings.Contains(p, want) {
			t.Fatalf("developer prompt lacks %q:\n%s", want, p)
		}
	}
}

func TestCustomPromptThatFailsFallsBackToDefault(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	var got []api.SettingsInvalidData
	h.e.o.Emit = func(kind string, data any) error {
		if kind == api.KindSettingsInvalid {
			mu.Lock()
			got = append(got, data.(api.SettingsInvalidData))
			mu.Unlock()
		}
		return nil
	}
	if err := h.st.PutPromptIf("", "developer", "{{.Nope}}"); err != nil {
		t.Fatal(err)
	}
	_, dev := h.readyDeveloper("one")
	p, err := h.e.Ready(h.ctx, h.actor(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, "You are "+dev.Name+", the developer") {
		t.Fatalf("prompt is not the default:\n%s", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || got[0].File != "prompts/developer.md" || got[0].Message == "" {
		t.Fatalf("settings.invalid = %+v", got)
	}
}

func TestUserInstructionsAreInThePrompt(t *testing.T) {
	h := newHarness(t)
	if err := h.st.PutPromptIf("", "developer-instructions", "Always write tests first."); err != nil {
		t.Fatal(err)
	}
	_, dev := h.readyDeveloper("one")
	p, err := h.e.Ready(h.ctx, h.actor(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, "## Your instructions (from the user)\nAlways write tests first.") {
		t.Fatalf("prompt:\n%s", p)
	}
}

func TestReadyTwiceDoesNotRepeatNotifications(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	h.comment(board.User, task, api.CommentRequest{Body: "@developer first"})
	h.notifyNow()
	first, err := h.e.Ready(h.ctx, h.actor(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.e.Ready(h.ctx, h.actor(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "Notifications that arrived") || strings.Contains(second, "Notifications that arrived") {
		t.Fatalf("first:\n%s\nsecond:\n%s", first, second)
	}
	if !strings.HasPrefix(second, "You are ") {
		t.Fatalf("second ready lacks the prompt:\n%s", second)
	}
}

func TestReadyEndsTheHandshakeTimeout(t *testing.T) {
	h := newHarness(t)
	_, dev := h.readyDeveloper("one")
	h.clock.Advance(agents.ResolveDelivery(api.PromptDelivery{}).ReadyTimeout() * 2)
	h.e.checkStarting(h.ctx, h.clock.Now())
	if h.agent(dev.ID).isKilled() {
		t.Fatal("an agent that ran omo ai ready was killed for not running it")
	}
}

func TestReadyIsForAgents(t *testing.T) {
	h := newHarness(t)
	if _, err := h.e.Ready(h.ctx, board.User); errCodeOf(err) != api.CodeForbidden {
		t.Fatalf("user: %v", err)
	}
}

// TestCustomPromptErrorIsBounded renders hand-edited prompts that Render
// alone would quote from or render without bound: the settings.invalid
// message names the line and no text of the prompt, and the default is used.
func TestCustomPromptErrorIsBounded(t *testing.T) {
	const secret = "sk-SECRET-prompt"
	for name, text := range map[string]string{
		"quotes":    "one\n{{ .Title }} {{ \"" + secret + "\" \"" + secret + "\" }}",
		"unbounded": secret + strings.Repeat("{{ printf \"%0100000d\" 1 }}", 6),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			var mu sync.Mutex
			var got []api.SettingsInvalidData
			h.e.o.Emit = func(kind string, data any) error {
				if kind == api.KindSettingsInvalid {
					mu.Lock()
					got = append(got, data.(api.SettingsInvalidData))
					mu.Unlock()
				}
				return nil
			}
			if err := h.st.PutPromptIf("", "developer", text); err != nil {
				t.Fatal(err)
			}
			_, dev := h.readyDeveloper("one")
			p, err := h.e.Ready(h.ctx, h.actor(dev.ID))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(p, "You are "+dev.Name+", the developer") {
				t.Fatalf("prompt is not the default:\n%.200s", p)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(got) == 0 {
				t.Fatal("no settings.invalid")
			}
			for _, g := range got {
				if strings.Contains(g.Message, "SECRET") || strings.Contains(g.Message, "\n") || len(g.Message) > 300 {
					t.Fatalf("settings.invalid message %q", g.Message)
				}
			}
		})
	}
}

func TestRolePromptHasSkillSetBlocks(t *testing.T) {
	h := newHarness(t)
	if err := h.st.PutPromptIf("", "developer-instructions", "Always write tests first."); err != nil {
		t.Fatal(err)
	}
	// An absolute folder of the platform: "/srv/skills.git" has no drive
	// on Windows and is no absolute path there.
	source := testpath.Abs("/srv/skills.git")
	put := func(name string, enabled bool, text map[string]string) {
		t.Helper()
		if err := h.st.PutSkillSetIf("", api.SkillSet{Name: name, Repository: source, Update: api.SkillSetPinned, Enabled: enabled, Instructions: text}); err != nil {
			t.Fatal(err)
		}
	}
	put("sp", true, map[string]string{"developer": "Read SKILLS.md first. {{.Nope}}"})
	put("off", false, map[string]string{"developer": "disabled", "reviewer": "disabled"})
	put("unfetched", true, map[string]string{"developer": "no checkout"})
	for _, name := range []string{"sp", "off"} {
		if err := skillsets.RecordSource(filepath.Join(h.layout.Skills, name), source, ""); err != nil {
			t.Fatal(err)
		}
	}
	block := "Skill set sp (checked out at " + filepath.Join(h.layout.Skills, "sp") + "; read-only, omo updates it):\nRead SKILLS.md first. {{.Nope}}"

	task, dev := h.readyDeveloper("one")
	p, err := h.e.Ready(h.ctx, h.actor(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, "## Your instructions (from the user)\nAlways write tests first.\n\n"+block) {
		t.Fatalf("developer prompt:\n%s", p)
	}
	if strings.Contains(p, "disabled") || strings.Contains(p, "no checkout") {
		t.Fatalf("a disabled or unfetched set reached the prompt:\n%s", p)
	}
	// The reviewer has no text of an enabled set: no block.
	rp, err := h.e.rolePrompt(h.ctx, board.Actor{Role: api.RoleReviewer, Ticket: task, Name: "Rita", Session: "s-rev", ReviewRound: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rp, "Skill set") {
		t.Fatalf("reviewer prompt has a skill set block:\n%s", rp)
	}
	// A customized role prompt gets the blocks through its Instructions.
	if err := h.st.PutPromptIf("", "developer", "Custom.\n{{.Instructions}}"); err != nil {
		t.Fatal(err)
	}
	p, err = h.e.rolePrompt(h.ctx, h.actor(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	if p != "Custom.\nAlways write tests first.\n\n"+block {
		t.Fatalf("customized prompt:\n%s", p)
	}
}
