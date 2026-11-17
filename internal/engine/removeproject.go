package engine

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// maxWorkLines is the most commits a refused removal lists.
const maxWorkLines = 20

// RemoveProject takes a project off ticket n for a (the user, or the CEO):
// the board checks who may and when (board.ClaimProjectRemoval) and claims
// the ticket; omo then removes what it made for the ticket in that project
// and the board records the removal (board.RemoveProject), with a comment
// that says what went.
//
// What omo made goes only when nothing is lost without the user saying so:
// the branch is deleted when it is the ticket's own (checkOwnBranch; a
// branch omo does not own is kept and the comment says why) at the commit
// it is at (compare-and-swap); commits of it that its base branch does not
// contain, and uncommitted changes in the worktree, refuse the removal with
// api.ProjectWork as details unless req.Discard is set. A worktree a user
// terminal holds refuses it too. Agents of the ticket that only wait in that
// project are ended first. A project whose folder is missing cannot be
// checked: its record goes, its worktree folder and branch stay as they
// are, and the comment says where they are.
func (e *Engine) RemoveProject(ctx context.Context, a board.Actor, n int64, req api.RemoveProjectRequest) (api.Ticket, error) {
	r, err := e.b.ClaimProjectRemoval(ctx, a, n, req.Project)
	if err != nil {
		return api.Ticket{}, err
	}
	recorded := false
	defer func() {
		if !recorded {
			if err := e.b.ReleaseClaim(context.WithoutCancel(ctx), n); err != nil {
				e.log.Printf("engine: release #%d after a project removal: %v", n, err)
			}
		}
		e.Wake()
	}()
	note, err := e.dropProjectWork(context.WithoutCancel(ctx), r, req.Discard)
	if err != nil {
		return api.Ticket{}, err
	}
	t, err := e.b.RemoveProject(context.WithoutCancel(ctx), a, n, r.Project.ID, note)
	if err != nil {
		return api.Ticket{}, err
	}
	recorded = true
	if err := e.pins.forgetProject(n, r.Project.ID); err != nil {
		e.log.Printf("engine: forget the pins of #%d in project %s: %v", n, r.Project.ID, err)
	}
	if proj, ok := e.st.Project(r.Project.ID); ok && !proj.Missing {
		// The commits recorded for an export of the project before.
		if err := e.keepCommits(context.WithoutCancel(ctx), n, proj.Path); err != nil {
			e.log.Printf("engine: forget the commits of #%d in project %s: %v", n, r.Project.ID, err)
		}
	}
	return t, nil
}

// dropProjectWork removes the worktree and branch of r's ticket in r's
// project, as RemoveProject describes, and returns what it did in words.
func (e *Engine) dropProjectWork(ctx context.Context, r board.ProjectRemoval, discard bool) (string, error) {
	t, p := r.Ticket, r.Project
	if p.Branch == "" && p.Worktree == "" {
		return e.endParked(ctx, r, ""), nil
	}
	proj, ok := e.st.Project(p.ID)
	if !ok || proj.Missing {
		var kept []string
		if p.Worktree != "" {
			kept = append(kept, "its worktree folder "+code(p.Worktree))
		}
		if p.Branch != "" {
			kept = append(kept, "its branch "+code(p.Branch))
		}
		return e.endParked(ctx, r, fmt.Sprintf("The project's folder is missing, so omo could not check %s and left them as they are; omo no longer keeps track of them.",
			strings.Join(kept, " and "))), nil
	}
	repo := proj.Path
	g := e.git

	// Is the branch omo's to delete, and what would go with it?
	tip, own, why := "", false, ""
	if p.Branch != "" {
		if exists, err := g.BranchExists(repo, p.Branch); err != nil {
			return "", fmt.Errorf("read the branch %s of project %s: %w", p.Branch, p.ID, err)
		} else if exists {
			if err := e.checkOwnBranch(ctx, t, p, repo, p.Branch); err != nil {
				why = err.Error()
			} else if tip, err = g.RevParse(repo, "refs/heads/"+p.Branch+"^{commit}"); err != nil {
				return "", fmt.Errorf("read the branch %s of project %s: %w", p.Branch, p.ID, err)
			} else {
				own = true
			}
		}
	}
	work := api.ProjectWork{Project: p.ID, Branch: p.Branch, Base: p.Base, Commits: []string{}, Files: []string{}}
	if own {
		work.Commits = e.unmergedCommits(repo, tip, p.Base)
	}
	present := false
	if p.Worktree != "" {
		if _, err := os.Lstat(p.Worktree); err == nil {
			present = true
			files, err := g.Status(repo, p.Worktree)
			if err != nil {
				return "", api.Errorf(api.CodeConflict, "omo could not list the uncommitted changes of the worktree %s (%v); nothing was removed", showPath(p.Worktree), err)
			}
			work.Files = append(work.Files, files...)
		}
	}
	if (len(work.Commits) > 0 || len(work.Files) > 0) && !discard {
		return "", workRefusal(t.ID, work)
	}
	if present && discard {
		if why := e.unsafeToDiscard(repo, p.Worktree, p.Branch); why != "" {
			return "", api.Errorf(api.CodeConflict, "omo kept the worktree %s of #%d: %s; that is more than uncommitted changes. Remove it by hand if it may go, then remove the project again",
				showPath(p.Worktree), t.ID, why)
		}
	}

	// Checked again while it is removed; refused here first so that no
	// waiting agent is ended for nothing.
	if p.Worktree != "" && e.held(p.Worktree) {
		return "", api.Errorf(api.CodeConflict, "a terminal is open in the worktree %s; close it, then remove the project again", showPath(p.Worktree))
	}
	parked := e.endParked(ctx, r, "")
	var done []string
	if parked != "" {
		done = append(done, parked)
	}
	if p.Worktree != "" {
		var err error
		var dirty []string
		ran := e.removeUnheld(p.Worktree, func() {
			if discard {
				err = g.ForceRemoveWorktree(repo, p.Worktree)
			} else {
				dirty, err = g.RemoveWorktreeIfClean(repo, p.Worktree, p.Branch)
			}
		})
		switch {
		case !ran:
			return "", api.Errorf(api.CodeConflict, "a terminal is open in the worktree %s; close it, then remove the project again", showPath(p.Worktree))
		case err != nil:
			return "", api.Errorf(api.CodeConflict, "omo kept the worktree %s of #%d: %s; nothing was removed", showPath(p.Worktree), t.ID, refusal(err))
		case len(dirty) > 0:
			work.Files = dirty
			return "", workRefusal(t.ID, work)
		}
		e.forgetNote(noteKey(t.ID, p.Worktree))
		e.forgetTrust(p.Worktree)
		if present {
			if discard && len(work.Files) > 0 {
				done = append(done, fmt.Sprintf("omo removed its worktree %s and discarded these uncommitted changes:\n%s", code(p.Worktree), fileList(work.Files)))
			} else {
				done = append(done, fmt.Sprintf("omo removed its worktree %s.", code(p.Worktree)))
			}
		}
	}
	switch {
	case p.Branch == "":
	case why != "":
		done = append(done, fmt.Sprintf("omo kept the branch %s: %s.", code(p.Branch), why))
	case !own:
		// The branch was already gone.
	default:
		if err := g.DeleteBranch(repo, p.Branch, tip); err != nil {
			done = append(done, fmt.Sprintf("omo kept the branch %s: %v.", code(p.Branch), err))
			break
		}
		text := fmt.Sprintf("omo deleted its branch %s, which was at %s; `git branch %s %s` brings it back while git still has that commit.",
			code(p.Branch), code(tip), p.Branch, tip)
		if len(work.Commits) > 0 {
			text += fmt.Sprintf(" These commits of it were not in %s:\n%s", code(p.Base), strings.Join(prefixed(work.Commits), "\n"))
		}
		done = append(done, text)
	}
	return strings.Join(done, "\n\n"), nil
}

// endParked ends the sessions of r that wait in the project, before its
// worktree goes, and returns note followed by a sentence naming them.
func (e *Engine) endParked(ctx context.Context, r board.ProjectRemoval, note string) string {
	if len(r.Parked) == 0 {
		return note
	}
	names := make([]string, len(r.Parked))
	for i, s := range r.Parked {
		e.end(ctx, s.ID, fmt.Sprintf("project %s was removed from #%d", r.Project.ID, r.Ticket.ID))
		names[i] = s.Name
	}
	ended := fmt.Sprintf("omo ended %s, which waited in it.", strings.Join(names, ", "))
	if note == "" {
		return ended
	}
	return note + " " + ended
}

// unmergedCommits lists the commits of tip that the branch base does not
// contain, as ProjectWork does; a base omo cannot read counts as not
// containing any of them.
func (e *Engine) unmergedCommits(repo, tip, base string) []string {
	g := e.git
	if base != "" {
		if in, err := g.IsAncestor(repo, tip, "refs/heads/"+base); err == nil && in {
			return []string{}
		}
		commits, total, err := g.FirstParentLog(repo, tip, "refs/heads/"+base, maxWorkLines)
		if err == nil {
			out := make([]string, 0, len(commits)+1)
			for _, c := range commits {
				out = append(out, strings.TrimSpace(short(c.ID)+" "+cleanSubject(c.Subject)))
			}
			if more := total - len(commits); more > 0 {
				out = append(out, fmt.Sprintf("and %d more", more))
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return []string{fmt.Sprintf("%s (omo cannot compare it with the base branch %q)", short(tip), base)}
}

// workRefusal is the refusal of a removal that would delete work.
func workRefusal(n int64, w api.ProjectWork) error {
	var what []string
	if len(w.Commits) > 0 {
		what = append(what, fmt.Sprintf("its branch %q has commits that %q does not contain (%s)", w.Branch, w.Base, strings.Join(w.Commits, "; ")))
	}
	if len(w.Files) > 0 {
		what = append(what, fmt.Sprintf("its worktree has uncommitted changes (%s)", joinCapped(w.Files)))
	}
	e := api.Errorf(api.CodeConflict, "removing project %q from #%d would delete work: %s. Remove it with discard (omo ai ticket remove-project %d %s --discard) to delete that too, or keep the project",
		w.Project, n, strings.Join(what, "; "), n, w.Project)
	e.Details = w
	return e
}

// prefixed returns lines as a Markdown list.
func prefixed(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "- " + l
	}
	return out
}
