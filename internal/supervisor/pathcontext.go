package supervisor

import (
	"path/filepath"
	"sort"

	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

// PromptPaths returns deterministic absolute path references for templates.
// Repository keys are included in labels so job-creating roles can translate
// between an omo --repo value and its checkout.
func (s *Supervisor) PromptPaths(workdir string) []prompts.PathReference {
	return s.promptPaths(workdir, nil)
}

func (s *Supervisor) promptPaths(workdir string, integrations map[string]queue.IntegrationBranch) []prompts.PathReference {
	if workdir == "" {
		workdir = s.OfficeDir
	}
	candidates := []prompts.PathReference{
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
		path := cfg.Repos[key]
		if entry, ok := integrations[key]; ok && entry.Worktree != "" {
			path = entry.Worktree
		}
		candidates = append(candidates, prompts.PathReference{
			Label: "repo:" + key, Path: path, Description: "configured repository checkout (use " + key + " as the --repo value)",
		})
	}

	refs := make([]prompts.PathReference, 0, len(candidates))
	byPath := make(map[string]int, len(candidates))
	for _, ref := range candidates {
		clean := filepath.Clean(ref.Path)
		if index, exists := byPath[clean]; exists {
			refs[index].Label += " / " + ref.Label
			refs[index].Description += "; " + ref.Description
			continue
		}
		ref.Path = clean
		byPath[clean] = len(refs)
		refs = append(refs, ref)
	}
	return refs
}

// PromptPathsForJob replaces PM repository checkout references with the
// integration worktrees already initialized for that PM job.
func (s *Supervisor) PromptPathsForJob(workdir string, jobID int64) []prompts.PathReference {
	job, err := s.Jobs.Get(jobID)
	if err != nil || job.Role != "product_manager" {
		return s.PromptPaths(workdir)
	}
	return s.promptPaths(workdir, job.IntegrationBranches)
}
