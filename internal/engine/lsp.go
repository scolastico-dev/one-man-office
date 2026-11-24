package engine

import (
	"context"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/lsp"
)

// LSP exposes worktree snapshots, report subscriptions and issue suppression
// to delivery adapters. Agent authorization belongs to those adapters.
func (e *Engine) LSP() *lsp.Manager { return e.lsp }

// attachLSP receives only worktrees prepared and recorded by startWork. Git's
// authoritative records validate every root before the runtime sees it. The
// CEO, export jobs and projectless agents never attach a monitor.
func (e *Engine) attachLSP(ctx context.Context, r spawnRequest, session string) {
	if r.purpose != api.PurposeWork || r.ticket == 0 {
		return
	}
	roots := []string{}
	for _, p := range r.worktrees {
		if p.Worktree == "" || (r.role != api.RolePM && !sameWorktree(p.Worktree, r.workdir)) {
			continue
		}
		project, ok := e.st.Project(p.ID)
		if !ok {
			continue
		}
		if err := e.git.CheckWorktreeGitFile(ctx, project.Path, p.Worktree); err != nil {
			e.log.Printf("lsp: validate worktree %s: %v", p.Worktree, err)
			continue
		}
		roots = append(roots, p.Worktree)
	}
	if len(roots) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	en := e.entries[session]
	if en == nil || en.ending != "" || e.stopping {
		return
	}
	if err := e.lsp.Attach(session, roots); err != nil {
		e.log.Printf("lsp: attach managed worktrees: %v", err)
	}
}
