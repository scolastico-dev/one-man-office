package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/claudetrust"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// cleanup removes what the done ticket t still has on disk, project by
// project, and never anything that holds work:
//
//   - A worktree is removed only while it is clean (ignored files do not
//     count) and no user terminal holds it; otherwise it stays, and a
//     comment names the folder and the files once per run. Every refusal of
//     git (an operation in progress, commits only a detached HEAD holds, a
//     locked worktree) keeps it the same way.
//   - Completed: the branch is deleted once the worktree is gone, and only
//     while it is still at a commit its merge target contains.
//   - Exported: the branch is kept for its pull request.
//   - Cancelled: the branch is kept unless the user asked to delete it. With
//     "delete the branch", a dirty worktree keeps the worktree and the
//     branch (nothing is discarded); only the user's explicit "discard
//     uncommitted changes" removes a dirty worktree, and then exactly its
//     uncommitted changes are lost, which the comment lists. Deleting the
//     integration branch of a story names the tasks whose merged work goes
//     with it.
//
// What is gone is recorded on the ticket; the trust entries Claude profiles
// hold for a removed folder are pruned.
func (e *Engine) cleanup(ctx context.Context, t board.EngineTicket) {
	t, snap, ok, err := e.read(ctx, t.ID)
	if err != nil {
		e.log.Printf("engine: read #%d to clean it up: %v", t.ID, err)
		return
	}
	if !ok || t.Column != api.ColumnDone || t.Busy {
		return
	}
	for _, s := range snap.Sessions {
		if s.Ticket == t.ID {
			return // an agent still works there; it is ended first
		}
	}
	for _, p := range t.Projects {
		if p.Worktree != "" {
			e.cleanProject(ctx, t, snap, p)
		}
	}
}

// cleanProject cleans up the worktree and branch of t in project p.
func (e *Engine) cleanProject(ctx context.Context, t board.EngineTicket, snap board.EngineSnapshot, p board.EngineProject) {
	dir := p.Worktree
	if e.held(dir) {
		return
	}
	proj, ok := e.st.Project(p.ID)
	if !ok || proj.Missing {
		e.keep(ctx, t, dir, fmt.Sprintf("omo kept the worktree %s of #%d: project %s is missing, so omo cannot check it. omo removes it once the project is back and the worktree is clean.",
			code(dir), t.ID, code(p.ID)))
		return
	}
	repo := proj.Path
	cancelled := t.Resolution == api.ResolutionCancelled
	deleteBranch := cancelled && t.DeleteBranch && p.Branch != ""
	if deleteBranch && t.DiscardUncommitted {
		e.discard(ctx, t, repo, p)
		return
	}
	var dirty []string
	var err error
	if !e.removeUnheld(dir, func() { dirty, err = e.git.RemoveWorktreeIfClean(repo, dir, p.Branch) }) {
		return // a user took a hold meanwhile; the release retries
	}
	if err != nil {
		e.keep(ctx, t, dir, fmt.Sprintf("omo kept the worktree %s of #%d: %s. Resolve that there; omo removes the worktree once it can.",
			code(dir), t.ID, refusal(err)))
		return
	}
	if len(dirty) > 0 {
		e.keepDirty(ctx, t, p, dirty, deleteBranch)
		return
	}
	e.forgetNote(noteKey(t.ID, dir))
	e.forgetTrust(dir)

	branch := p.Branch
	switch {
	case branch == "":
	case t.Resolution == api.ResolutionCompleted:
		if e.deleteMerged(ctx, t, snap, repo, p) {
			branch = ""
		}
	case deleteBranch:
		if tip, ok := e.deleteCancelled(ctx, t, repo, branch); ok {
			e.comment(ctx, t.ID, e.deletedText(ctx, t, branch, tip, nil))
			branch = ""
		}
	}
	if err := e.b.SetBranch(ctx, t.ID, p.ID, branch, ""); err != nil {
		e.log.Printf("engine: record the cleanup of #%d in project %s: %v", t.ID, p.ID, err)
	}
}

// deleteMerged deletes the branch of completed ticket t in project p when
// its merge target contains the commit it is at now, and reports whether
// the branch is gone. A branch with commits its target does not contain is
// kept, and a comment on t names them.
func (e *Engine) deleteMerged(ctx context.Context, t board.EngineTicket, snap board.EngineSnapshot, repo string, p board.EngineProject) bool {
	g := e.git
	tip, err := g.RevParse(repo, "refs/heads/"+p.Branch+"^{commit}")
	if err != nil {
		if exists, xerr := g.BranchExists(repo, p.Branch); xerr == nil && !exists {
			return true // already gone
		}
		e.log.Printf("engine: read branch %s of #%d: %v", p.Branch, t.ID, err)
		return false
	}
	targets := []string{p.Base}
	if t.StoryID != 0 {
		targets = append([]string{integrationBranch(snap, t.StoryID, p.ID)}, targets...)
	}
	merged := false
	for _, target := range targets {
		if target == "" {
			continue
		}
		if ok, err := g.IsAncestor(repo, tip, "refs/heads/"+target); err == nil && ok {
			merged = true
			break
		}
	}
	if !merged && t.Type == api.TypeStory && p.Base != "" {
		if proj, ok := e.st.Project(p.ID); ok && proj.StoryMerge == api.StoryMergeSquash {
			// A squashed story is merged when merging it again would
			// change nothing.
			if res, err := g.PrepareMerge(repo, "refs/heads/"+p.Base, tip, gitops.StyleSquash, "check"); err == nil && res.AlreadyMerged {
				merged = true
			}
		}
	}
	if !merged {
		e.log.Printf("engine: kept branch %s of #%d: its merge target does not contain it", p.Branch, t.ID)
		target := targets[0]
		if target == "" {
			target = p.Base
		}
		if target != "" && e.noteOnce(fmt.Sprintf("%d|unmerged|%s|%s", t.ID, p.ID, tip)) {
			e.comment(ctx, t.ID, fmt.Sprintf("omo kept the branch %s of #%d (project %s): %s does not contain these commits of it:\n%s\n\nMerge or delete them by hand; omo leaves the branch alone.",
				code(p.Branch), t.ID, code(p.ID), code(target), e.commitList(repo, tip, "refs/heads/"+target)))
		}
		return false
	}
	if err := g.DeleteBranch(repo, p.Branch, tip); err != nil {
		e.log.Printf("engine: kept branch %s of #%d: %v", p.Branch, t.ID, err)
		return false
	}
	return true
}

// deleteCancelled deletes branch of cancelled ticket t at the commit it is
// at now; it reports that commit and whether the branch is gone.
func (e *Engine) deleteCancelled(ctx context.Context, t board.EngineTicket, repo, branch string) (string, bool) {
	g := e.git
	tip, err := g.RevParse(repo, "refs/heads/"+branch+"^{commit}")
	if err != nil {
		if exists, xerr := g.BranchExists(repo, branch); xerr == nil && !exists {
			return "", true
		}
		e.log.Printf("engine: read branch %s of #%d: %v", branch, t.ID, err)
		return "", false
	}
	if err := g.DeleteBranch(repo, branch, tip); err != nil {
		e.comment(ctx, t.ID, fmt.Sprintf("omo kept the branch %s of #%d instead of deleting it: %v", code(branch), t.ID, err))
		return "", false
	}
	return tip, true
}

// deletedText is the comment on a branch deleted on cancel; discarded
// lists the uncommitted files removed with its worktree.
func (e *Engine) deletedText(ctx context.Context, t board.EngineTicket, branch, tip string, discarded []string) string {
	var b strings.Builder
	if tip != "" {
		fmt.Fprintf(&b, "omo deleted the branch %s of #%d, as asked when it was cancelled. It was at %s; `git branch <name> %s` brings it back while git still has that commit.",
			code(branch), t.ID, code(tip), tip)
	} else {
		fmt.Fprintf(&b, "The branch %s of #%d no longer exists.", code(branch), t.ID)
	}
	if t.Type == api.TypeStory {
		tasks, err := e.storyTasks(ctx, t.ID, api.ResolutionCompleted)
		if err != nil {
			e.log.Printf("engine: list the tasks of story #%d: %v", t.ID, err)
		}
		if len(tasks) > 0 {
			nums := make([]string, len(tasks))
			for i, task := range tasks {
				nums[i] = fmt.Sprintf("#%d", task.Number)
			}
			fmt.Fprintf(&b, " It held the merged work of the completed tasks %s, which is discarded with it.", strings.Join(nums, ", "))
		}
	}
	if discarded != nil {
		if len(discarded) == 0 {
			b.WriteString("\n\nThe worktree had no uncommitted changes.")
		} else {
			fmt.Fprintf(&b, "\n\nThese uncommitted changes were discarded with the worktree:\n%s", fileList(discarded))
		}
	}
	return b.String()
}

// discard removes the worktree of cancelled ticket t in project p with its
// uncommitted changes and deletes its branch: the user cancelled with
// "delete the branch" and "discard uncommitted changes". It destroys
// nothing beyond that promise: a worktree whose HEAD holds commits the
// branch does not contain, that has submodules checked out with their own
// history, or that is locked is kept, with a comment.
func (e *Engine) discard(ctx context.Context, t board.EngineTicket, repo string, p board.EngineProject) {
	g := e.git
	dir := p.Worktree
	var files []string
	if _, err := os.Stat(dir); err == nil {
		files, err = g.Status(dir)
		if err != nil {
			e.keep(ctx, t, dir, fmt.Sprintf("omo kept the worktree %s of #%d: it could not list its uncommitted changes (%v).", code(dir), t.ID, err))
			return
		}
		if why := e.unsafeToDiscard(repo, dir, p.Branch); why != "" {
			e.keep(ctx, t, dir, fmt.Sprintf("omo kept the worktree %s and the branch %s of #%d although it was cancelled with \"discard uncommitted changes\": %s. That is more than uncommitted changes; remove it by hand if it may go.",
				code(dir), code(p.Branch), t.ID, why))
			return
		}
	}
	var err error
	if !e.removeUnheld(dir, func() { err = g.ForceRemoveWorktree(repo, dir) }) {
		return // a user took a hold meanwhile; the release retries
	}
	if err != nil {
		e.keep(ctx, t, dir, fmt.Sprintf("omo kept the worktree %s of #%d: %s.", code(dir), t.ID, refusal(err)))
		return
	}
	e.forgetNote(noteKey(t.ID, dir))
	e.forgetTrust(dir)
	branch := p.Branch
	if tip, ok := e.deleteCancelled(ctx, t, repo, branch); ok {
		if files == nil {
			files = []string{}
		}
		e.comment(ctx, t.ID, e.deletedText(ctx, t, branch, tip, files))
		branch = ""
	} else if len(files) > 0 {
		e.comment(ctx, t.ID, fmt.Sprintf("omo removed the worktree %s of #%d and discarded these uncommitted changes:\n%s", code(dir), t.ID, fileList(files)))
	}
	if err := e.b.SetBranch(ctx, t.ID, p.ID, branch, ""); err != nil {
		e.log.Printf("engine: record the cleanup of #%d in project %s: %v", t.ID, p.ID, err)
	}
}

// unsafeToDiscard says what a forced removal of the worktree dir would
// destroy beyond its uncommitted changes, or "".
func (e *Engine) unsafeToDiscard(repo, dir, branch string) string {
	g := e.git
	head, err := g.RevParse(dir, "HEAD^{commit}")
	if err != nil {
		return fmt.Sprintf("omo could not read its HEAD (%v)", err)
	}
	if branch == "" {
		return "no branch is recorded for it"
	}
	if ok, err := g.IsAncestor(repo, head, "refs/heads/"+branch); err != nil || !ok {
		return fmt.Sprintf("its HEAD (%s) holds commits that branch %s does not contain", short(head), branch)
	}
	if hasSubmoduleRepos(dir) {
		return "it has submodules with their own history checked out"
	}
	return ""
}

// hasSubmoduleRepos reports whether the linked worktree dir keeps
// submodule repositories in its own git directory, where a forced removal
// would delete them.
func hasSubmoduleRepos(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !ok {
		return false
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	_, err = os.Stat(filepath.Join(gitDir, "modules"))
	return err == nil
}

// keepDirty comments, once per run, on a worktree kept for its uncommitted
// changes; withBranch says the branch was kept too although the user asked
// to delete it.
func (e *Engine) keepDirty(ctx context.Context, t board.EngineTicket, p board.EngineProject, files []string, withBranch bool) {
	var text string
	if withBranch {
		text = fmt.Sprintf("#%d was cancelled with \"delete the branch\", but its worktree %s has uncommitted changes or untracked files, so omo kept the worktree and the branch %s and discarded nothing:\n%s\n\nKeep what you need, then remove the files; omo removes the worktree and deletes the branch once the worktree is clean.",
			t.ID, code(p.Worktree), code(p.Branch), fileList(files))
	} else {
		text = fmt.Sprintf("omo kept the worktree %s of #%d because it has uncommitted changes or untracked files:\n%s\n\nCommit or remove them; omo removes the worktree once it is clean.",
			code(p.Worktree), t.ID, fileList(files))
	}
	e.keep(ctx, t, p.Worktree, text)
}

// keep posts text on t, once per run for the worktree dir.
func (e *Engine) keep(ctx context.Context, t board.EngineTicket, dir, text string) {
	if !e.noteOnce(noteKey(t.ID, dir)) {
		return
	}
	e.log.Printf("engine: kept worktree %s of #%d", dir, t.ID)
	e.comment(ctx, t.ID, text)
}

func noteKey(n int64, dir string) string { return fmt.Sprintf("%d|%s", n, holdKey(dir)) }

// comment posts a system comment on ticket n.
func (e *Engine) comment(ctx context.Context, n int64, body string) {
	if _, err := e.b.Comment(ctx, board.System, n, api.CommentRequest{Body: body}); err != nil {
		e.log.Printf("engine: comment on #%d: %v", n, err)
	}
}

// refusal describes why git did not remove a worktree.
func refusal(err error) string {
	var unref *gitops.UnreferencedCommitsError
	switch {
	case errors.As(err, &unref) && len(unref.Commits) > 0:
		return fmt.Sprintf("its HEAD holds %d commit(s) that no branch, tag or remote contains (%s); put them on a branch",
			len(unref.Commits), short(unref.Commits[0]))
	case errors.Is(err, gitops.ErrOperationInProgress):
		return err.Error() + "; finish or abort it"
	case errors.Is(err, gitops.ErrWorktreeLocked):
		return err.Error()
	}
	return "git did not remove it: " + err.Error()
}

// forgetTrust removes the Claude folder-trust entries of dir from every
// account a Claude profile uses, as the profile's agents would see it.
func (e *Engine) forgetTrust(dir string) {
	g := e.st.General()
	done := map[string]bool{}
	for _, prof := range e.st.Profiles() {
		if prof.CLI != api.CLIClaude {
			continue
		}
		models := []api.ProfileModel{{}}
		if len(prof.Models) > 0 {
			models = models[:0]
			for _, m := range prof.Models {
				models = append(models, m)
			}
		}
		for _, m := range models {
			env, err := agents.EffectiveEnv(agents.Spec{
				CLI: prof.CLI, Command: prof.Command, Args: append(append([]string(nil), prof.Args...), m.Args...),
				AgentEnv: g.AgentEnv, ProfileEnv: prof.Env, ModelEnv: m.Env, AccountDir: expandAccountDir(prof.AccountDir),
				Dir: dir, PathPrefix: e.pathPrefix,
			})
			if err != nil {
				e.log.Printf("engine: forget the trust of %s for profile %s: %v", dir, prof.Name, err)
				continue
			}
			path, err := claudetrust.PathForEnv(env)
			if err != nil || done[path] {
				continue
			}
			done[path] = true
			if err := claudetrust.ForgetForEnv(env, dir); err != nil {
				e.log.Printf("engine: forget the trust of %s for profile %s: %v", dir, prof.Name, err)
			}
		}
	}
}
