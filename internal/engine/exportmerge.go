package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

const mergeCheckAttention = board.ExportCheckAttentionPrefix

// fetchedTarget is shared by every watch of this repository/remote/target in
// one round. Errors are shared too: a failed fetch must never reuse old refs.
type fetchedTarget struct {
	repo, remote, base, commit string
	err                        error
}

func awaitingMerge(t board.EngineTicket) bool {
	return t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageAwaitingMerge
}

// CheckExport requests one round for all waiting exports. Overlapping user
// requests share the running round, including its fetch failures.
func (e *Engine) CheckExport(ctx context.Context, a board.Actor, n int64) error {
	if a.Role != api.RoleUser {
		return api.Errorf(api.CodeForbidden, "only the user requests an export merge check")
	}
	t, err := e.b.Get(ctx, a, n)
	if err != nil {
		return err
	}
	if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageAwaitingMerge {
		return api.Errorf(api.CodeInvalidTransition, "#%d is not waiting for its exported work to merge", n)
	}
	paused, err := e.b.Paused(ctx)
	if err != nil {
		return err
	}
	if paused {
		return api.Errorf(api.CodeConflict, "resume omo before checking exports")
	}
	e.startExportCheck(ctx, e.now(), true)
	return nil
}

// checkExportsMerged judges a complete round from fetched remote target
// snapshots. Recorded commits survive branch deletion; the recorded export
// branch can also provide evidence after a rebase or follow-up commits.
func (e *Engine) checkExportsMerged(ctx context.Context) {
	e.exportCheckMu.Lock()
	defer e.exportCheckMu.Unlock()
	snap, err := e.b.EngineSnapshot(ctx)
	if err != nil {
		e.log.Printf("engine: read the board to check exports: %v", err)
		return
	}
	if snap.Paused {
		return
	}
	if e.exportCheckFailures == nil {
		e.exportCheckFailures = map[string]int{}
	}
	var fetched []fetchedTarget
	active := map[string]bool{}
	for _, t := range snap.Tickets {
		if ctx.Err() != nil {
			return
		}
		if !awaitingMerge(t) {
			continue
		}
		key := reviewKey(t.ID, t.ReviewRound)
		active[key] = true
		commits := map[string]string{}
		merged := true
		var failure error
		for _, p := range t.Projects {
			if ctx.Err() != nil {
				return
			}
			if p.ExportURL == "" {
				continue
			} // projects left out of this export
			commits[p.ID] = p.ExportCommit
			proj, ok := e.st.Project(p.ID)
			if !ok || proj.Missing {
				failure = fmt.Errorf("project %s is unavailable", p.ID)
				merged = false
				continue
			}
			if p.ExportCommit == "" || p.ExportTargetCommit == "" || p.Start == "" || p.ExportRemote == "" || p.ExportBase == "" {
				failure = fmt.Errorf("project %s has no complete record of its exported commit and target", p.ID)
				merged = false
				continue
			}
			pushed, found, err := e.pins.pushed(t.ID, p.ID)
			if err != nil || !found || pushed.Commit != p.ExportCommit || pushed.Target != p.ExportTargetCommit || board.ExportRemoteDisplay(pushed.Address) != p.ExportRemote {
				failure = fmt.Errorf("project %s has no matching record of its pushed destination", p.ID)
				merged = false
				continue
			}
			destination := pushRemote(pushed)
			result := -1
			for i, r := range fetched {
				// exact: recorded remote addresses and remote ref names identify one fetch.
				if r.remote != destination.URL || r.base != p.ExportBase {
					continue
				}
				if same, _ := gitops.SameRepository(r.repo, proj.Path); same {
					result = i
					break
				}
			}
			if result < 0 {
				remote := destination
				// Recover credentials from the current remote only if its display
				// address still matches the saved destination; a renamed remote is fine.
				if current, err := e.git.RemoteFor(ctx, proj.Path, p.ExportBase); err == nil && current.Address() == pushed.Address && exportSSHUser(current) == pushed.SSHUser {
					remote = current
				}
				e.at("export-check-fetch")
				id, err := e.git.FetchTarget(ctx, proj.Path, remote, p.ExportBase)
				fetched = append(fetched, fetchedTarget{repo: proj.Path, remote: destination.URL, base: p.ExportBase, commit: id, err: err})
				result = len(fetched) - 1
			}
			r := fetched[result]
			if r.err != nil {
				failure = fmt.Errorf("project %s: %w", p.ID, r.err)
				merged = false
				continue
			}
			// An unchanged target, or work already present at export time, is
			// never evidence of a newly landed pull request.
			if r.commit == p.ExportTargetCommit {
				merged = false
				continue
			}
			already, err := e.git.ExportContained(proj.Path, p.Start, p.ExportCommit, p.ExportTargetCommit)
			if err != nil {
				failure = fmt.Errorf("project %s: %w", p.ID, err)
				merged = false
				continue
			}
			if already {
				merged = false
				continue
			}
			contained, err := e.git.ExportContained(proj.Path, p.Start, p.ExportCommit, r.commit)
			if err == nil && !contained {
				contained, err = e.changedExportContained(proj.Path, p, r.commit)
			}
			if err != nil {
				failure = fmt.Errorf("project %s: %w", p.ID, err)
			}
			if err != nil || !contained {
				merged = false
			}
		}
		if len(commits) == 0 {
			merged = false
			failure = fmt.Errorf("no exported projects are recorded")
		}
		// A rejection or a later export must not receive an old result or flag.
		current, ok := e.ticketNow(ctx, t.ID)
		if !ok || !awaitingMerge(current) || current.ReviewRound != t.ReviewRound {
			continue
		}
		if failure != nil {
			e.exportCheckFailures[key]++
			message := mergeCheckAttention + gitops.HideCredentials(failure.Error())
			e.log.Printf("engine: check export of #%d: %s", t.ID, message)
			if e.exportCheckFailures[key] >= 3 {
				if err := e.b.SetExportCheckAttention(ctx, t.ID, t.ReviewRound, message); err != nil {
					e.log.Printf("engine: flag merge check of #%d: %v", t.ID, err)
				}
			}
			continue
		}
		delete(e.exportCheckFailures, key)
		if current.AttentionOwner == board.AttentionMergeCheck {
			if err := e.b.SetExportCheckAttention(ctx, t.ID, t.ReviewRound, ""); err != nil {
				e.log.Printf("engine: clear merge check flag of #%d: %v", t.ID, err)
			}
		}
		if merged {
			e.at("export-check-complete")
			if err := e.b.CompleteExportMerge(ctx, t.ID, t.ReviewRound, commits); err != nil {
				e.log.Printf("engine: record merged export of #%d: %v", t.ID, err)
			}
		}
	}
	for key := range e.exportCheckFailures {
		if !active[key] {
			delete(e.exportCheckFailures, key)
		}
	}
}

// changedExportContained checks the current tip of the recorded export branch
// when the original exported patch could not prove inclusion. A rebase may
// resolve a conflict differently, and a follow-up may replace exported lines.
// Compare that tip with the saved target baseline, never the local base branch:
// the local checkout need not have pulled the remote merge. Keep all saved
// export and diff commits intact for the round guard and the historical diff.
func (e *Engine) changedExportContained(repo string, p board.EngineProject, target string) (bool, error) {
	if p.ExportBranch == "" {
		return false, nil
	}
	if exists, err := e.git.BranchExists(repo, p.ExportBranch); err != nil || !exists {
		return false, err
	}
	tip, err := e.git.RevParse(repo, gitops.BranchRef(p.ExportBranch))
	if err != nil || tip == p.ExportCommit {
		return false, err
	}
	start, err := e.git.MergeBase(repo, p.ExportTargetCommit, tip)
	if err != nil {
		return false, err
	}
	// Empty patches and changes already included at export are not evidence.
	if already, err := e.git.ExportContained(repo, start, tip, p.ExportTargetCommit); err != nil || already {
		return false, err
	}
	return e.git.ExportContained(repo, start, tip, target)
}

// checkExportsDue schedules nothing while paused, disabled, or without work.
func (e *Engine) checkExportsDue(ctx context.Context, now time.Time) {
	e.startExportCheck(ctx, now, false)
}

// startExportCheck coalesces simultaneous timer and manual requests into one
// round; no request queues a second fetch behind the active round.
func (e *Engine) startExportCheck(ctx context.Context, now time.Time, force bool) {
	interval := time.Duration(e.st.General().ExportCheckInterval)
	if !force && interval <= 0 {
		return
	}
	snap, err := e.b.EngineSnapshot(ctx)
	if err != nil || snap.Paused {
		return
	}
	waiting := false
	for _, t := range snap.Tickets {
		if awaitingMerge(t) {
			waiting = true
			break
		}
	}
	if !waiting {
		return
	}
	d := &e.disk
	d.mu.Lock()
	due := !d.exportsRunning && (force || d.exportsChecked.IsZero() || now.Sub(d.exportsChecked) >= interval)
	if due {
		d.exportsRunning = true
		d.exportsChecked = now
	}
	workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	if due {
		d.exportsCancel = cancel
	}
	d.mu.Unlock()
	if !due {
		cancel()
		return
	}
	started := e.launch(workerCtx, 0, nil, func(ctx context.Context) {
		defer func() { cancel(); d.mu.Lock(); d.exportsRunning = false; d.exportsCancel = nil; d.mu.Unlock() }()
		e.checkExportsMerged(ctx)
	})
	if !started {
		cancel()
		d.mu.Lock()
		d.exportsRunning = false
		d.exportsCancel = nil
		d.mu.Unlock()
	}
}
