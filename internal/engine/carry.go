package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// An approving verdict holds for the commit it was given for. omo keeps,
// per ticket and project, the commit of the project's last approval (the
// commit pinned at the hand-in of that round, see pinStore.reviewed) in
// the pins file. In a later review round a project whose pinned commit is
// still that commit needs no reviewer: its approval carries over. A
// project of a story whose integration branch has no commits of its own
// compared with its base branch needs no reviewer either and is approved
// with a note, the first time only. (A task's branch without commits still
// gets a reviewer: a task that changed nothing is for a reviewer to judge.) A rejection removes the record, so it never carries over; a
// round still needs every project approved, carried over or fresh.

// approval is the commit of a project an approving verdict holds for.
type approval struct {
	Commit string `json:"commit"`
	// Round is the review round the approval was given in.
	Round int `json:"round"`
	// NoChanges is set for a project approved because its branch had no
	// commits of its own.
	NoChanges bool `json:"no_changes,omitempty"`
}

// carried is a project whose approval carries over into the current round.
type carried struct {
	project string
	// from is the approval that carries over; first is set for a branch
	// without commits of its own that has no approval yet (from is then
	// the approval to record).
	from  approval
	first bool
}

// recordApprovals returns approved (by project) updated with the verdicts
// of t's current review round: an approval holds for the project's pinned
// commit of that round, a rejection removes the record. An approval of a
// commit already recorded keeps its record (and its round). changed reports
// whether anything changed.
func recordApprovals(t board.EngineTicket, pins map[string]string, approved map[string]approval) (map[string]approval, bool) {
	out := map[string]approval{}
	for k, v := range approved {
		out[k] = v
	}
	changed := false
	for _, p := range t.Projects {
		switch t.Verdicts[p.ID] {
		case "approve":
			commit := pins[p.ID]
			if commit == "" || out[p.ID].Commit == commit {
				continue
			}
			out[p.ID] = approval{Commit: commit, Round: t.ReviewRound}
			changed = true
		case "reject":
			if _, ok := out[p.ID]; ok {
				delete(out, p.ID)
				changed = true
			}
		}
	}
	return out, changed
}

// carryOver returns the projects of t, which is in Review at stage agent,
// that have no verdict in the current round and need no reviewer: their
// pinned commit is the commit of their last approval, or, for a story,
// their integration branch has no commits of its own (noOwn). Projects
// without a pinned commit get a reviewer.
func carryOver(t board.EngineTicket, pins map[string]string, approved map[string]approval, noOwn func(project, commit string) bool) []carried {
	if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageAgent {
		return nil
	}
	var out []carried
	for _, p := range t.Projects {
		commit := pins[p.ID]
		if t.Verdicts[p.ID] != "" || commit == "" {
			continue
		}
		if a, ok := approved[p.ID]; ok && a.Commit == commit {
			out = append(out, carried{project: p.ID, from: a})
			continue
		}
		if t.Type == api.TypeStory && noOwn != nil && noOwn(p.ID, commit) {
			out = append(out, carried{project: p.ID, from: approval{Commit: commit, Round: t.ReviewRound, NoChanges: true}, first: true})
		}
	}
	return out
}

// carriedText is the note on a round's carried-over approvals; "" when
// nothing needs saying (only branches without commits of their own that
// were noted before).
func carriedText(round int, cs []carried) string {
	var lines []string
	for _, c := range cs {
		switch {
		case c.first:
			lines = append(lines, fmt.Sprintf("- %s: no commits of its own compared with the branch it was cut from; approved without a reviewer", c.project))
		case !c.from.NoChanges:
			lines = append(lines, fmt.Sprintf("- %s: approved in round %d, unchanged", c.project, c.from.Round))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("Review round %d needs no reviewer for these projects:\n%s", round, strings.Join(lines, "\n"))
}

// carryVerdicts records the approvals of the tickets of snap and carries
// approvals over into the current round of every ticket that waits for its
// reviewers (see carryOver). It reports whether it changed the board; the
// pass then plans again on a fresh snapshot. Tickets a worker acts on
// (inflight) or that are claimed are left for a later pass.
func (e *Engine) carryVerdicts(ctx context.Context, snap board.EngineSnapshot, inflight map[int64]bool) bool {
	changed := false
	for _, t := range snap.Tickets {
		if t.Column != api.ColumnReview && t.Column != api.ColumnInProgress {
			continue
		}
		pins, err := e.pins.reviewed(t.ID, t.ReviewRound)
		if err != nil {
			e.log.Printf("engine: read the recorded hand-ins: %v", err)
			return changed
		}
		if pins == nil {
			continue
		}
		approved, err := e.pins.approvals(t.ID)
		if err != nil {
			e.log.Printf("engine: read the recorded approvals: %v", err)
			return changed
		}
		if next, ok := recordApprovals(t, pins, approved); ok {
			if err := e.pins.setApprovals(t.ID, next); err != nil {
				e.log.Printf("engine: record the approvals of #%d: %v", t.ID, err)
				continue
			}
			approved = next
		}
		if t.Busy || inflight[t.ID] || t.FailedStep != api.FailedNone {
			continue
		}
		cs := carryOver(t, pins, approved, func(project, commit string) bool { return e.noOwnCommits(t, project, commit) })
		if len(cs) == 0 {
			continue
		}
		projects := make([]string, 0, len(cs))
		next := map[string]approval{}
		for k, v := range approved {
			next[k] = v
		}
		for _, c := range cs {
			projects = append(projects, c.project)
			next[c.project] = c.from
		}
		sort.Strings(projects)
		if err := e.pins.setApprovals(t.ID, next); err != nil {
			e.log.Printf("engine: record the approvals of #%d: %v", t.ID, err)
			continue
		}
		if err := e.b.CarryVerdicts(ctx, t.ID, t.ReviewRound, projects, carriedText(t.ReviewRound, cs)); err != nil {
			e.log.Printf("engine: carry the approvals of #%d over: %v", t.ID, err)
			continue
		}
		changed = true
	}
	return changed
}

// noOwnCommits reports whether commit, the pinned commit of story t in
// project, is contained in the project's base branch. Any doubt (a missing project or branch, a git error) counts as
// having commits of its own, so a reviewer looks at it.
func (e *Engine) noOwnCommits(t board.EngineTicket, project, commit string) bool {
	var base string
	for _, p := range t.Projects {
		if p.ID == project {
			base = p.Base
		}
	}
	proj, ok := e.st.Project(project)
	if base == "" || !ok || proj.Missing {
		return false
	}
	in, err := e.git.IsAncestor(proj.Path, commit, "refs/heads/"+base)
	if err != nil {
		e.log.Printf("engine: check whether #%d has commits of its own in project %s: %v", t.ID, project, err)
		return false
	}
	return in
}
