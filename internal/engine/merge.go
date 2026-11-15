package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// maxRebuilds is how often one merge commit is built again because its
// target branch moved between building and advancing it.
const maxRebuilds = 3

// mergeTarget is one branch a ticket merges into: the story's integration
// branch for a task of a story, the base branch for a standalone task, and
// each project's base branch for a story.
type mergeTarget struct {
	project string
	repo    string
	// target is the branch that is advanced, source the ticket's branch.
	target, source string
	style          gitops.MergeStyle
	message        string

	// from is the commit target pointed at when the merge was built;
	// commit is the commit it is advanced to.
	from, commit string
	// conflicts lists the paths that do not merge.
	conflicts []string
	// merged is set once target contains source.
	merged bool
}

// mergeProblem is a merge that cannot go on without a person; reason says
// what to do.
type mergeProblem struct{ reason string }

func (p *mergeProblem) Error() string { return p.reason }

// errConflict marks a target whose merge, built again, now conflicts.
var errConflict = errors.New("the merge conflicts")

// merger carries one merge of a ticket through its steps.
type merger struct {
	e       *Engine
	ctx     context.Context
	n       int64
	claimed bool
}

// runMerge merges ticket t, which is in Review at stage merging, into its
// target branches. It claims the ticket, builds the merge of every target
// without touching any checkout, ref or index, and only when none of them
// conflicts advances the targets one after the other, in project order. A
// target that already contains the ticket's branch (from an earlier attempt
// that was interrupted or blocked) is skipped, so running the merge again is
// always safe. Every way it ends is recorded: CompleteMerge when all targets
// contain the work (then the ticket's agents end and its worktrees are
// cleaned up), MergeConflict when one conflicts, a failed merge step naming
// what a person has to do otherwise. The claim ends on every path.
func (e *Engine) runMerge(ctx context.Context, t board.EngineTicket) {
	if err := e.b.ClaimMerge(ctx, t.ID); err != nil {
		e.log.Printf("engine: claim #%d to merge it: %v", t.ID, err)
		return
	}
	m := &merger{e: e, ctx: ctx, n: t.ID, claimed: true}
	done := func() bool {
		defer m.finish()
		return m.run()
	}()
	if done {
		e.afterMerge(ctx, t.ID)
	}
}

// finish releases a claim the merge still holds.
func (m *merger) finish() {
	if !m.claimed {
		return
	}
	if err := m.e.b.ReleaseClaim(context.WithoutCancel(m.ctx), m.n); err != nil {
		m.e.log.Printf("engine: release the claim on #%d: %v", m.n, err)
	}
	m.claimed = false
}

// run carries out the merge and reports whether the ticket is now done.
func (m *merger) run() bool {
	e := m.e
	t, snap, ok, err := e.read(m.ctx, m.n)
	if err != nil {
		e.log.Printf("engine: read #%d: %v", m.n, err)
		return false
	}
	if !ok || t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageMerging || !t.Busy {
		return false
	}
	targets, err := m.targets(t, snap)
	if err != nil {
		m.fail(t, nil, err)
		return false
	}
	// Every target is built, even after one failed, so the reason of a
	// failure knows which targets contain the work already.
	var first error
	for i := range targets {
		if err := m.prepare(&targets[i]); err != nil && first == nil {
			first = failure(&targets[i], err)
		}
	}
	if first != nil {
		m.fail(t, targets, first)
		return false
	}
	if conflicted(targets) {
		m.conflict(t, targets)
		return false
	}
	for i := range targets {
		tg := &targets[i]
		if tg.merged {
			continue
		}
		if err := m.advance(tg); err != nil {
			if errors.Is(err, errConflict) {
				m.conflict(t, targets)
			} else {
				m.fail(t, targets, failure(tg, err))
			}
			return false
		}
	}
	if err := e.b.CompleteMerge(m.ctx, m.n); err != nil {
		e.log.Printf("engine: record the merge of #%d: %v", m.n, err)
		return false
	}
	m.claimed = false
	return true
}

// targets lists the branches ticket t merges into, in project order.
func (m *merger) targets(t board.EngineTicket, snap board.EngineSnapshot) ([]mergeTarget, error) {
	e := m.e
	if len(t.Projects) == 0 {
		return nil, &mergeProblem{fmt.Sprintf("#%d has no project to merge into.", t.ID)}
	}
	var out []mergeTarget
	for _, p := range t.Projects {
		proj, ok := e.st.Project(p.ID)
		if !ok || !proj.Enabled || proj.Missing {
			return nil, &mergeProblem{fmt.Sprintf("Project %s of #%d is missing or disabled, so omo cannot merge it. Restore or enable the project, then retry.", p.ID, t.ID)}
		}
		if p.Branch == "" {
			return nil, &mergeProblem{fmt.Sprintf("#%d has no branch in project %s to merge.", t.ID, p.ID)}
		}
		tg := mergeTarget{project: p.ID, repo: proj.Path, source: p.Branch, target: p.Base, style: gitops.StyleMerge,
			message: fmt.Sprintf("Merge #%d: %s", t.ID, t.Title)}
		switch {
		case t.StoryID != 0:
			tg.target = integrationBranch(snap, t.StoryID, p.ID)
			if tg.target == "" {
				return nil, &mergeProblem{fmt.Sprintf("Story #%d has no integration branch in project %s to merge #%d into.", t.StoryID, p.ID, t.ID)}
			}
		case t.Type == api.TypeStory && proj.StoryMerge == api.StoryMergeSquash:
			tg.style = gitops.StyleSquash
			tg.message = m.squashMessage(t)
		}
		if tg.target == "" {
			return nil, &mergeProblem{fmt.Sprintf("#%d has no base branch in project %s to merge into.", t.ID, p.ID)}
		}
		out = append(out, tg)
	}
	return out, nil
}

// integrationBranch returns the branch story has in project, or "".
func integrationBranch(snap board.EngineSnapshot, story int64, project string) string {
	for _, st := range snap.Tickets {
		if st.ID != story {
			continue
		}
		for _, sp := range st.Projects {
			if sp.ID == project {
				return sp.Branch
			}
		}
	}
	return ""
}

// squashMessage is the message of a story squashed into one commit: its
// title and number, and the tasks it completed.
func (m *merger) squashMessage(t board.EngineTicket) string {
	msg := fmt.Sprintf("%s (#%d)", t.Title, t.ID)
	tasks, err := m.e.storyTasks(m.ctx, t.ID, api.ResolutionCompleted)
	if err != nil {
		m.e.log.Printf("engine: list the tasks of story #%d: %v", t.ID, err)
		return msg
	}
	var lines []string
	for _, task := range tasks {
		lines = append(lines, fmt.Sprintf("- #%d: %s", task.Number, task.Title))
	}
	if len(lines) > 0 {
		msg += "\n\nTasks:\n" + strings.Join(lines, "\n")
	}
	return msg
}

// storyTasks returns the done tasks of story with resolution, oldest first.
func (e *Engine) storyTasks(ctx context.Context, story int64, resolution api.Resolution) ([]api.TicketSummary, error) {
	s := int(story)
	list, err := e.b.List(ctx, board.System, api.TicketFilter{Story: &s, Column: api.ColumnDone, DoneLimit: 500})
	if err != nil {
		return nil, err
	}
	var out []api.TicketSummary
	for i := len(list.Tickets) - 1; i >= 0; i-- {
		task := list.Tickets[i]
		if task.Type == api.TypeTask && task.Resolution == resolution {
			out = append(out, task)
		}
	}
	return out, nil
}

// prepare builds the merge of tg's source into its target as it is now,
// without touching any checkout, index or ref.
func (m *merger) prepare(tg *mergeTarget) error {
	g := m.e.git
	tg.from, tg.commit, tg.conflicts, tg.merged = "", "", nil, false
	from, err := g.RevParse(tg.repo, "refs/heads/"+tg.target+"^{commit}")
	if err != nil {
		return &mergeProblem{fmt.Sprintf("Branch %s of project %s does not exist, so #%d cannot be merged into it. Restore it, then retry.", tg.target, tg.project, m.n)}
	}
	source, err := g.RevParse(tg.repo, "refs/heads/"+tg.source+"^{commit}")
	if err != nil {
		return &mergeProblem{fmt.Sprintf("Branch %s of #%d no longer exists in project %s. Restore it, then retry.", tg.source, m.n, tg.project)}
	}
	res, err := g.PrepareMerge(tg.repo, from, source, tg.style, tg.message)
	if err != nil {
		return err
	}
	tg.from, tg.commit, tg.conflicts, tg.merged = from, res.Commit, res.Conflicts, res.AlreadyMerged
	return nil
}

// advance moves tg's target to the prepared commit. A target that moved
// meanwhile gets its merge built again, at most maxRebuilds times.
func (m *merger) advance(tg *mergeTarget) error {
	for rebuilds := 0; ; rebuilds++ {
		m.e.at("advance")
		err := m.e.git.AdvanceBranch(tg.repo, tg.target, tg.from, tg.commit)
		if err == nil {
			tg.merged = true
			return nil
		}
		if !errors.Is(err, gitops.ErrBranchMoved) {
			return err
		}
		if rebuilds == maxRebuilds {
			return &mergeProblem{fmt.Sprintf("%s (project %s) kept moving while omo merged #%d into it (%d attempts). Retry once nothing else commits to it.",
				tg.target, tg.project, m.n, maxRebuilds+1)}
		}
		if err := m.prepare(tg); err != nil {
			return err
		}
		if len(tg.conflicts) > 0 {
			return errConflict
		}
		if tg.merged {
			return nil
		}
	}
}

func conflicted(targets []mergeTarget) bool {
	for _, tg := range targets {
		if len(tg.conflicts) > 0 {
			return true
		}
	}
	return false
}

// conflict sends ticket t back to work with a comment naming what
// conflicted: a task to its developer, a story to its product manager.
func (m *merger) conflict(t board.EngineTicket, targets []mergeTarget) {
	var b strings.Builder
	if t.Type == api.TypeStory {
		fmt.Fprintf(&b, "Merging story #%d into its base branches conflicts, so omo merged nothing further.\n", t.ID)
		for _, tg := range targets {
			if len(tg.conflicts) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\nProject %s, %s into %s, conflicts in:\n%s\n", code(tg.project), code(tg.source), code(tg.target), fileList(tg.conflicts))
			fmt.Fprintf(&b, "Create a task in project %s whose developer merges %s into its branch and resolves the conflict, then hand the story in again.\n",
				code(tg.project), code(tg.target))
		}
	} else {
		for _, tg := range targets {
			if len(tg.conflicts) == 0 {
				continue
			}
			fmt.Fprintf(&b, "Merging #%d into %s (project %s) conflicts in:\n%s\n\nMerge %s into your branch %s, resolve the conflicts, commit and run `omo ai done`.\n",
				t.ID, code(tg.target), code(tg.project), fileList(tg.conflicts), code(tg.target), code(tg.source))
		}
	}
	if note := progressNote(targets); note != "" {
		fmt.Fprintf(&b, "\n%s\n", note)
	}
	if err := m.e.b.MergeConflict(m.ctx, m.n, b.String()); err != nil {
		m.e.log.Printf("engine: record the merge conflict of #%d: %v", m.n, err)
		return
	}
	m.claimed = false
}

// fail records a merge that cannot go on without a person as the ticket's
// failed merge step. The reason also says which projects are merged
// already: a retry merges only the others.
func (m *merger) fail(t board.EngineTicket, targets []mergeTarget, err error) {
	var p *mergeProblem
	reason := ""
	if errors.As(err, &p) {
		reason = p.reason
	} else {
		reason = fmt.Sprintf("Merging #%d failed: %v. Fix the cause, then retry.", t.ID, err)
	}
	if note := progressNote(targets); note != "" {
		reason += " " + note
	}
	if serr := m.e.b.SetFailed(m.ctx, m.n, api.FailedMerge, reason); serr != nil {
		m.e.log.Printf("engine: flag #%d: %v (merge: %s)", m.n, serr, reason)
		return
	}
	m.claimed = false
}

// progressNote names, for a ticket with several targets of which some
// contain the work, those that do and those still to merge.
func progressNote(targets []mergeTarget) string {
	var done, left []string
	for _, tg := range targets {
		name := fmt.Sprintf("%s of project %s", tg.target, tg.project)
		if tg.merged {
			done = append(done, name)
		} else {
			left = append(left, name)
		}
	}
	if len(done) == 0 || len(targets) < 2 {
		return ""
	}
	note := "Already merged: " + strings.Join(done, ", ") + "."
	if len(left) > 0 {
		note += " Not merged yet: " + strings.Join(left, ", ") + "; a retry merges only these."
	}
	return note
}

// failure turns an error of preparing or advancing tg into the reason a
// person reads: what stands in the way and what to do about it.
func failure(tg *mergeTarget, err error) error {
	var p *mergeProblem
	if errors.As(err, &p) {
		return err
	}
	where := fmt.Sprintf("%s (project %s)", tg.target, tg.project)
	var blocked *gitops.BlockedError
	var switched *gitops.SwitchedError
	var stale *gitops.StaleCheckoutError
	var reason string
	switch {
	case errors.As(err, &blocked) && len(blocked.Files) > 0:
		reason = fmt.Sprintf("Uncommitted changes to %s in %s block merging into %s. Commit, stash or discard them there, then retry.",
			joinCapped(blocked.Files), blocked.Worktree, where)
		if blocked.Incomplete {
			reason += " The list may be incomplete; git said: " + strings.TrimSpace(blocked.Output)
		}
	case errors.As(err, &blocked):
		reason = fmt.Sprintf("%s: %s, which blocks merging into %s. Finish or abort it there, then retry.",
			blocked.Worktree, strings.TrimSpace(blocked.Output), where)
	case errors.As(err, &switched):
		reason = fmt.Sprintf("%s switched to %s while omo merged into %s, so git fast-forwarded %s instead; %s did not move. Check %s there, then retry.",
			switched.Worktree, switched.Head, where, switched.Head, tg.target, switched.Head)
	case errors.As(err, &stale):
		reason = fmt.Sprintf("%s contains the merge now, but it became checked out in %s meanwhile and the files there are still at %s, so git status there shows the merged changes reverted. Update that folder (git read-tree -m -u %s %s there keeps local changes), then retry.",
			where, stale.Worktree, short(stale.From), stale.From, stale.To)
	case errors.Is(err, gitops.ErrSignatureNotVerified):
		reason = fmt.Sprintf("git refused to fast-forward %s to omo's merge commit: merge.verifySignatures is set and the commit has no trusted signature. Let omo sign its merge commits (a signing key and commit.gpgsign in the agent environment) or switch the check off, then retry. %v",
			where, err)
	case errors.Is(err, gitops.ErrTimeout):
		reason = fmt.Sprintf("Merging into %s timed out: %v. Check what holds the repository (omo never removes an index.lock itself), then retry.", where, err)
	default:
		reason = fmt.Sprintf("Merging into %s failed: %v.", where, err)
		if lower := strings.ToLower(err.Error()); strings.Contains(lower, "gpg") || strings.Contains(lower, "sign") {
			reason += " omo signs its merge commits when commit.gpgsign is set; make sure the signing key works without a prompt."
		}
		reason += " Fix the cause, then retry."
	}
	return &mergeProblem{reason}
}

// afterMerge ends the agents of the merged ticket n and cleans up its
// worktrees, in the merge's worker.
func (e *Engine) afterMerge(ctx context.Context, n int64) {
	t, snap, ok, err := e.read(ctx, n)
	if err != nil || !ok {
		return
	}
	for _, s := range snap.Sessions {
		if s.Ticket == n {
			e.end(ctx, s.ID, "merged")
		}
	}
	e.cleanup(ctx, t)
}

// short abbreviates a commit id for a message.
func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
