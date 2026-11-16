package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// An export handles a ticket's projects one by one. A project whose branch
// has no commits of its own compared with its base branch is left out
// (one comment names those projects). Every other project is pushed and
// gets its export job; a project whose push fails, whose job reports a
// failure or keeps crashing has failed, which does not stop the others.
// Once no project is left with an export going on, the ticket is settled:
//
//   - every project that is not left out has its pull request: Done and
//     exported, with the pull requests' addresses;
//   - some failed: Review at stage human with attention (and a comment)
//     naming each failed project and why; the projects exported keep their
//     pull request. Approving with export again exports only the projects
//     without one (no second push or pull request for the others); "Merge
//     locally instead" merges the projects without a pull request;
//   - every project was left out: nothing to export, Done as completed.
//
// What was left out and what failed is kept with the pins (see pinStore);
// a pull request's address is kept on the board.

// projectExport is where the export of one project of a ticket stands.
type projectExport int

const (
	exportPending projectExport = iota
	exportDone
	exportLeftOut
	exportFailedState
)

// exportFailed records that the export of ticket n failed with msg, for
// the projects msg names: the ticket stays in Review and waits for the user
// (retry the export, merge locally or reject); its export jobs end on the
// next pass.
func (e *Engine) exportFailed(ctx context.Context, n int64, msg string) {
	if err := e.b.ExportFailed(ctx, n, msg); err != nil {
		e.log.Printf("engine: record the failed export of #%d: %v", n, err)
	}
}

// projectExportFailed records that the export of project of ticket n
// failed with reason, then settles the ticket's export if nothing else is
// going on.
func (e *Engine) projectExportFailed(ctx context.Context, n int64, project, reason string) {
	if err := e.keepExportFailure(ctx, n, project, reason); err != nil {
		e.log.Printf("engine: record the failed export of #%d in project %s: %v", n, project, err)
		e.exportFailed(ctx, n, fmt.Sprintf("Export of %s failed: %s", project, reason))
		return
	}
	if t, ok := e.ticketNow(ctx, n); ok {
		e.settleExport(ctx, t)
	}
}

// keepExportFailure keeps why the export of project of ticket n failed:
// with the pins, which the engine settles the export from, and on the
// ticket's project, which answers and events show. reason is cleaned of
// credentials first. These two helpers are the only writers of both (a
// guard test checks it).
func (e *Engine) keepExportFailure(ctx context.Context, n int64, project, reason string) error {
	reason = gitops.HideCredentials(reason)
	if err := e.pins.setExportFailed(n, project, reason); err != nil {
		return err
	}
	return e.b.SetProjectExport(ctx, n, project, api.ExportFailedState, reason)
}

// keepLeftOut keeps that project of ticket n has nothing to export in the
// review round: with the pins and on the ticket's project.
func (e *Engine) keepLeftOut(ctx context.Context, n int64, round int, project string) error {
	if err := e.pins.setLeftOut(n, round, project); err != nil {
		return err
	}
	return e.b.SetProjectExport(ctx, n, project, api.ExportLeftOut, "")
}

// exportStates returns where the export of every project of t stands.
func (e *Engine) exportStates(t board.EngineTicket) (map[string]projectExport, map[string]string, error) {
	states, reasons := map[string]projectExport{}, map[string]string{}
	for _, p := range t.Projects {
		if p.ExportURL != "" {
			states[p.ID] = exportDone
			continue
		}
		left, failed, err := e.pins.exportOutcome(t.ID, t.ReviewRound, p.ID)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case left:
			states[p.ID] = exportLeftOut
		case failed != "":
			states[p.ID], reasons[p.ID] = exportFailedState, failed
		default:
			states[p.ID] = exportPending
		}
	}
	return states, reasons, nil
}

// exportSettled returns, per ticket of snap that is exporting, the projects
// whose export is over without a pull request (left out or failed): they
// get no export job.
func (e *Engine) exportSettled(snap board.EngineSnapshot) map[int64]map[string]bool {
	out := map[int64]map[string]bool{}
	for _, t := range snap.Tickets {
		if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting {
			continue
		}
		states, _, err := e.exportStates(t)
		if err != nil {
			e.log.Printf("engine: read the exports of #%d: %v", t.ID, err)
			continue
		}
		for p, st := range states {
			if st == exportLeftOut || st == exportFailedState {
				if out[t.ID] == nil {
					out[t.ID] = map[string]bool{}
				}
				out[t.ID][p] = true
			}
		}
	}
	return out
}

// settleExports leaves out, for every ticket of snap that is exporting and
// no worker acts on, the projects without commits of their own, and
// settles its export once no project is left with an export going on. It
// reports whether it changed the board.
func (e *Engine) settleExports(ctx context.Context, snap board.EngineSnapshot, inflight map[int64]bool) bool {
	changed := false
	for _, t := range snap.Tickets {
		if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting || t.Busy || inflight[t.ID] ||
			t.FailedStep != api.FailedNone || (t.Type == api.TypeTask && t.StoryID != 0) {
			continue
		}
		if e.settleExport(ctx, t) {
			changed = true
		}
	}
	return changed
}

// settleExport does settleExports for ticket t and reports whether it
// changed the board.
func (e *Engine) settleExport(ctx context.Context, t board.EngineTicket) bool {
	e.exportMu.Lock()
	defer e.exportMu.Unlock()
	if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting {
		return false
	}
	states, reasons, err := e.exportStates(t)
	if err != nil {
		e.log.Printf("engine: read the exports of #%d: %v", t.ID, err)
		return false
	}
	pins, err := e.pins.reviewed(t.ID, t.ReviewRound)
	if err != nil {
		e.log.Printf("engine: read the recorded hand-ins: %v", err)
		return false
	}
	changed := false
	var left []string
	for _, p := range t.Projects {
		if states[p.ID] != exportPending || pins[p.ID] == "" || !e.noOwnCommits(t, p.ID, pins[p.ID]) {
			continue
		}
		if err := e.keepLeftOut(ctx, t.ID, t.ReviewRound, p.ID); err != nil {
			e.log.Printf("engine: record that #%d leaves out project %s: %v", t.ID, p.ID, err)
			return false
		}
		states[p.ID] = exportLeftOut
		left = append(left, p.ID)
	}
	if len(left) > 0 {
		var lines []string
		for _, p := range left {
			lines = append(lines, "- "+p+": no commits of its own compared with its base branch")
		}
		e.comment(ctx, t.ID, "The export leaves out these projects, which have nothing to export:\n"+strings.Join(lines, "\n"))
		changed = true
	}
	var failed []string
	exported := 0
	for _, p := range t.Projects {
		switch states[p.ID] {
		case exportPending:
			return changed
		case exportDone:
			exported++
		case exportFailedState:
			failed = append(failed, fmt.Sprintf("Export of %s failed: %s", p.ID, reasons[p.ID]))
		}
	}
	switch {
	case len(failed) > 0:
		if err := e.b.ExportFailed(ctx, t.ID, strings.Join(failed, "; ")); err != nil {
			e.log.Printf("engine: record the failed export of #%d: %v", t.ID, err)
			return changed
		}
		// This export attempt is over: approving with export again tries
		// the failed projects afresh.
		if err := e.pins.clearExportFailed(t.ID); err != nil {
			e.log.Printf("engine: forget the failed exports of #%d: %v", t.ID, err)
		}
	default:
		if err := e.b.FinishExport(ctx, t.ID); err != nil {
			e.log.Printf("engine: finish the export of #%d: %v", t.ID, err)
			return changed
		}
	}
	return true
}

// checkExportsMerged looks, without contacting any forge, whether exported
// tickets have reached their base branches locally: when every project's
// exported branch is an ancestor of (or equal to) its base branch, the
// ticket's export counts as merged and the tickets depending on it may
// start. A branch that is gone, a squash merge or a project that is missing
// cannot be seen here; the user marks those as merged.
func (e *Engine) checkExportsMerged(ctx context.Context) {
	e.disk.mu.Lock()
	e.disk.exportsChecked = e.now()
	e.disk.mu.Unlock()
	snap, err := e.b.EngineSnapshot(ctx)
	if err != nil {
		e.log.Printf("engine: read the board to check exports: %v", err)
		return
	}
	for _, t := range snap.Tickets {
		if t.Column != api.ColumnDone || t.Resolution != api.ResolutionExported || t.ExportMerged || len(t.Projects) == 0 {
			continue
		}
		merged := true
		for _, p := range t.Projects {
			if !e.exportedBranchMerged(p.ID, p.Branch, p.Base) {
				merged = false
				break
			}
		}
		if !merged {
			continue
		}
		if err := e.b.SetExportMerged(ctx, t.ID); err != nil {
			e.log.Printf("engine: record that the export of #%d is merged: %v", t.ID, err)
		}
	}
}

// exportedBranchMerged reports whether branch is contained in base in the
// project's repository.
func (e *Engine) exportedBranchMerged(project, branch, base string) bool {
	if branch == "" || base == "" {
		return false
	}
	proj, ok := e.st.Project(project)
	if !ok || proj.Missing {
		return false
	}
	for _, b := range []string{branch, base} {
		if exists, err := e.git.BranchExists(proj.Path, b); err != nil || !exists {
			return false
		}
	}
	ok, err := e.git.IsAncestor(proj.Path, "refs/heads/"+branch, "refs/heads/"+base)
	if err != nil {
		e.log.Printf("engine: check whether %s is merged into %s in project %s: %v", branch, base, project, err)
		return false
	}
	return ok
}

// checkExportsDue starts checkExportsMerged in a worker once the rescan
// interval has passed since the last check.
func (e *Engine) checkExportsDue(ctx context.Context, now time.Time) {
	interval := time.Duration(e.st.General().RescanInterval)
	d := &e.disk
	d.mu.Lock()
	due := !d.exportsRunning && (d.exportsChecked.IsZero() || interval <= 0 || now.Sub(d.exportsChecked) >= interval)
	if due {
		d.exportsRunning, d.exportsChecked = true, now
	}
	d.mu.Unlock()
	if !due {
		return
	}
	started := e.launch(context.WithoutCancel(ctx), 0, nil, func(ctx context.Context) {
		defer func() {
			d.mu.Lock()
			d.exportsRunning = false
			d.mu.Unlock()
		}()
		e.checkExportsMerged(ctx)
	})
	if !started {
		d.mu.Lock()
		d.exportsRunning = false
		d.mu.Unlock()
	}
}
