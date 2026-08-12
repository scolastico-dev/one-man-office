package supervisor

import (
	"fmt"
	"sort"
	"strings"
)

// RepoContext describes the office's repositories to the roles that decide
// where work goes. An office is either one repository or a directory holding
// several (a microservice landscape); the CEO must know which, because
// cross-service work has to be split into one developer job per repo.
func (s *Supervisor) RepoContext() string {
	keys := make([]string, 0, len(s.Cfg.Repos))
	for k := range s.Cfg.Repos {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("REPOSITORIES IN THIS OFFICE\n")
	switch len(keys) {
	case 0:
		b.WriteString("This office has no repositories configured yet, so no developer\n" +
			"jobs can be created. Tell the user to run `omo repo add <path>`.\n")
		return b.String()
	case 1:
		fmt.Fprintf(&b, "This office works on a single repository:\n  %-16s %s\n", keys[0], s.Cfg.Repos[keys[0]])
	default:
		fmt.Fprintf(&b, "This office spans %d repositories (a microservice landscape):\n", len(keys))
		for _, k := range keys {
			fmt.Fprintf(&b, "  %-16s %s\n", k, s.Cfg.Repos[k])
		}
	}
	b.WriteString("\nUse the key (not the path) as --repo when creating a developer job:\n" +
		"  omo job create --role developer --repo " + keys[0] + " --title \"…\" --goal-file <path>\n" +
		"Each developer job works in exactly one repository, in its own git\n" +
		"worktree on its own branch. A freelancer job may also use --repo to\n" +
		"receive an isolated worktree for repository-scoped research or artifacts.\n")
	if len(keys) > 1 {
		b.WriteString("\nWork that touches several services must be split into one job per\n" +
			"repository. Fix the interface contract between them in the spec first\n" +
			"(likely routes, payloads, types) so the jobs can run in parallel;\n" +
			"mismatches found later become small follow-up jobs, not blockers.\n")
	}
	return b.String()
}
