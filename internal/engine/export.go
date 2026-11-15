package engine

import (
	"context"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// exportFailed records that the export of ticket n failed with msg: an
// export job that keeps crashing. The ticket stays in Review and waits for
// the user (retry the export, merge locally or reject); its export jobs end
// on the next pass, so the crash loop ends here.
func (e *Engine) exportFailed(ctx context.Context, n int64, msg string) {
	if err := e.b.ExportFailed(ctx, n, msg); err != nil {
		e.log.Printf("engine: record the failed export of #%d: %v", n, err)
	}
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
