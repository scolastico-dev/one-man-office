package server

import (
	"context"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/discover"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// What started a scan (api.ScanReport.Trigger).
const (
	scanStart        = "start"
	scanTimer        = "timer"
	scanRootsChanged = "roots_changed"
	scanButton       = "button"
)

// reservedIDsTimeout bounds the board query for the project ids tickets use.
const reservedIDsTimeout = 5 * time.Second

// scanSeams let tests replace how the scanner reads folders and how long it
// waits between timer scans. nil fields keep the real behaviour.
type scanSeams struct {
	readDir  func(string) ([]os.DirEntry, error)
	interval func(configured time.Duration) time.Duration
}

// scanRun is one scan, queued or running. done is closed when it ended;
// report is set before then unless the scan was stopped by shutdown.
type scanRun struct {
	trigger string
	// forced is set when the user asked for it: it runs even without
	// workspace roots.
	forced bool
	// skipped is set when it started and found nothing to do: it has no
	// report, so the button does not join it.
	skipped bool
	waiting int // requests waiting for it (tests)
	done    chan struct{}
	report  *api.ScanReport
}

// scanner searches the workspace roots for repositories and adds them as
// projects. One goroutine (run) does every scan, so two never overlap. A
// trigger while a scan runs queues at most one more; the button joins the
// running scan instead.
type scanner struct {
	s     *Server
	seams scanSeams
	wake  chan struct{} // capacity 1

	mu      sync.Mutex
	current *scanRun // running
	queued  *scanRun // next, at most one
	roots   []settings.WorkspaceRoot
	last    *api.ScanReport
	count   int // scans done or running
}

func newScanner(s *Server, seams *scanSeams) *scanner {
	sc := &scanner{s: s, wake: make(chan struct{}, 1)}
	if seams != nil {
		sc.seams = *seams
	}
	return sc
}

// request asks for a scan and returns the run that will answer it: the
// running scan for the button, else the queued one, created when needed.
func (sc *scanner) request(trigger string) *scanRun {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.requestLocked(trigger)
}

func (sc *scanner) requestLocked(trigger string) *scanRun {
	run := sc.queued
	switch {
	case trigger == scanButton && sc.current != nil && !sc.current.skipped:
		run = sc.current
	case run == nil:
		run = &scanRun{trigger: trigger, done: make(chan struct{})}
		sc.queued = run
		select {
		case sc.wake <- struct{}{}:
		default:
		}
	}
	if trigger == scanButton {
		run.forced = true
	}
	return run
}

// settingsChanged starts a scan when a change of the general settings
// changed the workspace roots compared with the last scan.
func (sc *scanner) settingsChanged(c settings.Change) {
	if sc == nil || c.Section != settings.SectionGeneral || c.Err != nil || c.Removed {
		return
	}
	roots := sc.s.settings.General().WorkspaceRoots
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sameRoots(roots, sc.roots) {
		return
	}
	sc.requestLocked(scanRootsChanged)
}

func sameRoots(a, b []settings.WorkspaceRoot) bool {
	return slices.EqualFunc(a, b, func(x, y settings.WorkspaceRoot) bool {
		return x.Path == y.Path && x.MaxDepth == y.MaxDepth && slices.Equal(x.Ignore, y.Ignore)
	})
}

// state is the answer of GET /api/projects/scan.
func (sc *scanner) state() api.ScanState {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	running := sc.queued != nil || sc.current != nil && !sc.current.skipped
	return api.ScanState{Running: running, Last: sc.last}
}

// started returns the number of scans done or running.
func (sc *scanner) started() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.count
}

// waiting returns the number of requests waiting for the running scan.
func (sc *scanner) waiting() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.current == nil {
		return 0
	}
	return sc.current.waiting
}

// interval is the time between timer scans: rescan_interval, read anew
// before each wait.
func (sc *scanner) interval() time.Duration {
	d := time.Duration(sc.s.settings.General().RescanInterval)
	if sc.seams.interval != nil {
		d = sc.seams.interval(d)
	}
	if d <= 0 {
		d = time.Minute
	}
	return d
}

// run does the scans until ctx ends. It scans once at the start, every
// rescan interval after the last scan, and on every request. A run left
// queued at the end is released without a report.
func (sc *scanner) run(ctx context.Context) {
	timer := time.NewTimer(sc.interval())
	defer timer.Stop()
	defer sc.abandon()
	for {
		sc.mu.Lock()
		run := sc.queued
		sc.queued = nil
		sc.current = run
		sc.mu.Unlock()
		if run != nil {
			sc.scan(ctx, run)
			if ctx.Err() != nil {
				return
			}
			timer.Reset(sc.interval())
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-sc.wake:
		case <-timer.C:
			sc.request(scanTimer)
		}
	}
}

// abandon releases the runs that will never be done.
func (sc *scanner) abandon() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for _, r := range []*scanRun{sc.current, sc.queued} {
		if r != nil {
			select {
			case <-r.done:
			default:
				close(r.done)
			}
		}
	}
	sc.current, sc.queued = nil, nil
}

// scan does one run. An automatic scan without workspace roots does
// nothing; a scan stopped by shutdown records nothing.
func (sc *scanner) scan(ctx context.Context, run *scanRun) {
	defer func() {
		sc.mu.Lock()
		if run.report != nil {
			sc.last = run.report
		}
		sc.current = nil
		sc.mu.Unlock()
		close(run.done)
	}()
	roots := sc.s.settings.General().WorkspaceRoots
	sc.mu.Lock()
	sc.roots = roots
	run.skipped = len(roots) == 0 && !run.forced
	if !run.skipped {
		sc.count++
	}
	skipped := run.skipped
	sc.mu.Unlock()
	if skipped {
		return
	}
	report := api.ScanReport{Trigger: run.trigger, StartedAt: time.Now().UTC(), Roots: []api.RootScan{}, Added: []string{}}
	for _, r := range roots {
		rs := sc.scanRoot(ctx, r)
		if ctx.Err() != nil {
			return
		}
		report.Roots = append(report.Roots, rs)
		report.Added = append(report.Added, rs.Added...)
	}
	for _, p := range sc.s.settings.Projects(true) {
		if p.Missing {
			report.Missing++
		}
	}
	report.FinishedAt = time.Now().UTC()
	run.report = &report
	if err := sc.s.appendSystemEvent(api.KindProjectsScanned, report); err != nil {
		sc.s.log.Printf("record %s event: %v", api.KindProjectsScanned, err)
	}
	sc.s.events.Wake()
}

// scanRoot walks one root, outside every lock, and adds what it found. The
// omo home and the agents' worktrees are never entered.
func (sc *scanner) scanRoot(ctx context.Context, r settings.WorkspaceRoot) api.RootScan {
	layout := sc.s.opts.Layout
	res := discover.Walk(ctx, discover.Root{Path: settings.ExpandPath(r.Path), MaxDepth: r.MaxDepth, Ignore: r.Ignore},
		discover.Options{Skip: []string{layout.Root, layout.Worktrees}, ReadDir: sc.seams.readDir})
	rs := api.RootScan{
		Path: r.Path, State: res.State, Message: res.Message, Visited: res.Visited,
		Found: len(res.Repos), Added: []string{}, Skipped: res.Skipped, Links: res.Links,
	}
	if rs.Skipped == nil {
		rs.Skipped = []string{}
	}
	if ctx.Err() != nil || len(res.Repos) == 0 {
		return rs
	}
	found := make([]settings.Found, len(res.Repos))
	for i, repo := range res.Repos {
		found[i] = settings.Found{Path: repo.Path, Rel: repo.Rel}
	}
	added, err := sc.s.settings.DiscoverProjects(r.Path, found)
	for _, p := range added {
		rs.Added = append(rs.Added, p.ID)
	}
	if err != nil {
		sc.s.log.Printf("add the projects found below %s: %v", r.Path, err)
		rs.State = discover.StateFailed
		rs.Message = "the projects found could not all be saved: " + err.Error()
	}
	return rs
}

// reservedProjectIDs returns the project ids tickets reference, for the
// settings store; on an error it logs and returns none.
func (s *Server) reservedProjectIDs() map[string]bool {
	if s.board == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, reservedIDsTimeout)
	defer cancel()
	ids, err := s.board.ProjectIDsInUse(ctx)
	if err != nil {
		s.log.Printf("read the project ids tickets use: %v", err)
		return nil
	}
	return ids
}

// registerDiscovery adds the scan and folder routes.
func (s *Server) registerDiscovery(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"GET /api/projects/scan", s.getScan, userOnly},
		{"POST /api/projects/rescan", s.postRescan, userOnly},
		{"GET /api/folders", s.getFolders, userOnly},
	})
}

func (s *Server) getScan(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.scan.state())
}

// postRescan starts a scan, or joins the running one, and answers its
// report once it is done.
func (s *Server) postRescan(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	sc := s.scan
	sc.mu.Lock()
	run := sc.requestLocked(scanButton)
	run.waiting++
	sc.mu.Unlock()
	defer func() {
		sc.mu.Lock()
		run.waiting--
		sc.mu.Unlock()
	}()
	select {
	case <-run.done:
	case <-r.Context().Done():
		return
	case <-s.ctx.Done():
	}
	sc.mu.Lock()
	report := run.report
	sc.mu.Unlock()
	if report == nil {
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is stopping; the scan did not finish"))
		return
	}
	writeJSON(w, http.StatusOK, report)
}
