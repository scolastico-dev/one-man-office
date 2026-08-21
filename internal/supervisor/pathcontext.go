package supervisor

import (
	"path/filepath"
	"sort"

	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

// PromptPaths returns deterministic absolute path references for templates.
// Repository keys are included in labels so job-creating roles can translate
// between an omo --repo value and its checkout.
func (s *Supervisor) PromptPaths(workdir string) []prompts.PathReference {
	if workdir == "" {
		workdir = s.OfficeDir
	}
	refs := []prompts.PathReference{
		{Label: "office_root", Path: s.OfficeDir, Description: "folder containing the .omo office directory"},
		{Label: "omo_dir", Path: filepath.Join(s.OfficeDir, ".omo"), Description: "supervisor-owned office state; do not inspect except documented safe paths"},
		{Label: "storage", Path: filepath.Join(s.OfficeDir, ".omo", "storage"), Description: "shared agent storage and coordination workspace"},
		{Label: "workspace", Path: workdir, Description: "this agent's intended current working directory"},
	}
	cfg := s.Config()
	keys := make([]string, 0, len(cfg.Repos))
	for key := range cfg.Repos {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		refs = append(refs, prompts.PathReference{
			Label: "repo:" + key, Path: cfg.Repos[key], Description: "configured repository checkout (use " + key + " as the --repo value)",
		})
	}
	return refs
}
