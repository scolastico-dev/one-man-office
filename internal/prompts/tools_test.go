package prompts_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

// toolSpan is a code span that names an MCP tool: a snake_case name, or
// any name right after the word "tool".
var toolSpan = regexp.MustCompile("tool `([a-z_]+)`|`([a-z]+(?:_[a-z]+)+)`")

// rolePrompts are the role prompts and the agent sessions that read them.
var rolePrompts = map[string]aicmd.Audience{
	"ceo":       {Role: api.RoleCEO, Purpose: api.PurposeWork},
	"pm":        {Role: api.RolePM, Purpose: api.PurposeWork},
	"developer": {Role: api.RoleDeveloper, Purpose: api.PurposeWork},
	"reviewer":  {Role: api.RoleReviewer, Purpose: api.PurposeWork},
	"export":    {Role: api.RoleDeveloper, Purpose: api.PurposeExport},
}

// Every tool a default prompt names is an MCP tool that the sessions
// reading it see. Each role prompt names some, and only as tools the
// session may have: an agent program can lack them, so the prompt says
// "if you have" and keeps `omo ai` as the way that always works.
func TestPromptToolNamesExist(t *testing.T) {
	tools := map[string]aicmd.Command{}
	for _, c := range aicmd.Commands() {
		if !c.CommandLineOnly {
			tools[c.Tool] = c
		}
	}
	for name, text := range prompts.Defaults() {
		audience, isRole := rolePrompts[name]
		named := 0
		for _, m := range toolSpan.FindAllStringSubmatch(text, -1) {
			tool := m[1] + m[2]
			c, ok := tools[tool]
			if !ok {
				t.Errorf("%s names the tool %q, which omo does not offer over MCP", name, tool)
				continue
			}
			named++
			if isRole && !c.Sees(audience) {
				t.Errorf("%s names the tool %q, which its sessions do not see", name, tool)
			}
		}
		if !isRole {
			continue
		}
		if named == 0 {
			t.Errorf("%s names no omo tool", name)
		}
		if !strings.Contains(text, "If you have the tools of the MCP server `omo`") || !strings.Contains(text, "`omo ai` always works") {
			t.Errorf("%s does not say that the tools may be missing and `omo ai` always works", name)
		}
	}
	for name := range rolePrompts {
		if _, ok := prompts.Defaults()[name]; !ok {
			t.Errorf("no default prompt %s", name)
		}
	}
}

// `omo ai wait` is no tool: every role prompt that teaches it says to run
// it in the terminal.
func TestPromptsKeepWaitInTheTerminal(t *testing.T) {
	for name := range rolePrompts {
		text := prompts.Defaults()[name]
		if name == "ceo" {
			continue // the CEO never waits
		}
		if !strings.Contains(text, "There is no tool for waiting: `omo ai wait` always runs in the terminal.") {
			t.Errorf("%s does not keep `omo ai wait` in the terminal", name)
		}
	}
}
