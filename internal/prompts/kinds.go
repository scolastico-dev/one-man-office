package prompts

import (
	"slices"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// roleNames are the roles that have a role prompt.
var roleNames = []string{"ceo", "pm", "developer", "reviewer", "export"}

// instructionsSuffix ends the name of a role's instructions prompt.
const instructionsSuffix = "-instructions"

// Kind returns the kind of a prompt: api.PromptKindRole for a role prompt,
// api.PromptKindInstructions for "<role>-instructions",
// api.PromptKindMessage for the other names in Names, and "" for a name omo
// does not know.
func Kind(name string) string {
	switch {
	case slices.Contains(roleNames, name):
		return api.PromptKindRole
	case RoleOf(name) != "":
		return api.PromptKindInstructions
	case slices.Contains(Names, name):
		return api.PromptKindMessage
	}
	return ""
}

// RoleOf returns the role of an instructions prompt ("developer" for
// "developer-instructions") and "" for every other name.
func RoleOf(name string) string {
	role, ok := strings.CutSuffix(name, instructionsSuffix)
	if !ok || !slices.Contains(roleNames, role) {
		return ""
	}
	return role
}
