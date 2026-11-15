package prompts

import (
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestKinds(t *testing.T) {
	counts := map[string]int{}
	for _, name := range Names {
		k := Kind(name)
		if k == "" {
			t.Errorf("%s has no kind", name)
		}
		counts[k]++
	}
	if counts[api.PromptKindRole] != 5 || counts[api.PromptKindInstructions] != 5 || counts[api.PromptKindMessage] != 7 {
		t.Fatalf("counts = %v", counts)
	}
	for name, want := range map[string]string{
		"developer": api.PromptKindRole, "export": api.PromptKindRole,
		"developer-instructions": api.PromptKindInstructions,
		"start":                  api.PromptKindMessage, "question-reminder": api.PromptKindMessage,
		"ghost": "", "ghost-instructions": "", "": "",
	} {
		if got := Kind(name); got != want {
			t.Errorf("Kind(%q) = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{
		"developer-instructions": "developer", "ceo-instructions": "ceo", "export-instructions": "export",
		"developer": "", "start": "", "ghost-instructions": "", "-instructions": "",
	} {
		if got := RoleOf(name); got != want {
			t.Errorf("RoleOf(%q) = %q, want %q", name, got, want)
		}
	}
}
