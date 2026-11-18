package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/discover"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// scanBase is a temporary folder under the name a scan reports for it. A
// scan resolves a root's links before it walks it, and what it finds lies
// below the resolved name: on Windows the temporary folder has a short name
// (RUNNER~1), on macOS it lies behind a link. Tests that compare a found
// path with a path they built start from the resolved name.
func scanBase(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return base
}

// scanHome isolates OMO_HOME in a folder of its own below base.
func scanHome(t *testing.T, base string) globalhome.Layout {
	t.Helper()
	t.Setenv("OMO_HOME", filepath.Join(base, "omo-home"))
	l, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// writeRoots writes settings.yaml with the given workspace roots before the
// server opens the home.
func writeRoots(t *testing.T, l globalhome.Layout, roots ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("workspace_roots:\n")
	for _, r := range roots {
		b.WriteString("  - path: " + r + "\n    max_depth: 4\n")
	}
	if err := os.WriteFile(l.Settings, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func openScanServer(t *testing.T, l globalhome.Layout, seams *scanSeams) *Server {
	t.Helper()
	s, err := newServer(Options{Layout: l, Config: testConfig(), Version: "test", scan: seams}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// gitInit makes dir a git repository without reading the developer's git
// configuration.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q", dir)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// waitScanned waits until a scan with trigger has finished and its changes
// are in the event log, and returns its report. The settings store reports a
// found project while the scan still runs, but the event of that change is
// written by a worker after the fact; a test that reads the last event id
// next would otherwise count that event as its own.
func waitScanned(t *testing.T, s *Server, trigger string) api.ScanReport {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st := s.scan.state()
		if st.Last != nil && st.Last.Trigger == trigger {
			s.waitSettingsEvents(context.Background())
			return *st.Last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s scan finished; state %+v", trigger, s.scan.state())
	return api.ScanReport{}
}

func projectByPath(s *Server, path string) (settings.Project, bool) {
	for _, p := range s.settings.Projects(true) {
		if p.Path == path {
			return p, true
		}
	}
	return settings.Project{}, false
}

func TestScanAtStart(t *testing.T) {
	base := scanBase(t)
	root := filepath.Join(base, "work")
	gitInit(t, filepath.Join(root, "a", "one"))
	gitInit(t, filepath.Join(root, "b", "two"))
	gitInit(t, filepath.Join(root, "node_modules", "x"))
	l := scanHome(t, base)
	if err := os.WriteFile(l.Settings, []byte("workspace_roots:\n  - path: "+root+"\n    max_depth: 4\n    ignore: [node_modules]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := openScanServer(t, l, nil)
	rep := waitScanned(t, s, scanStart)
	if len(rep.Roots) != 1 || rep.Roots[0].Path != root || rep.Roots[0].State != "ok" || rep.Roots[0].Found != 2 {
		t.Fatalf("report %+v", rep)
	}
	if len(rep.Added) != 2 || !slices.Equal(rep.Added, rep.Roots[0].Added) {
		t.Fatalf("added %v", rep.Added)
	}
	for _, rel := range []string{"a/one", "b/two"} {
		p, ok := projectByPath(s, filepath.Join(root, filepath.FromSlash(rel)))
		if !ok || p.Name != rel || p.Root != root || !p.Enabled {
			t.Fatalf("project for %s: %+v %v", rel, p, ok)
		}
	}
	if _, ok := projectByPath(s, filepath.Join(root, "node_modules", "x")); ok {
		t.Fatal("an ignored folder became a project")
	}
	evs := wantEvents(t, s, api.KindProjectsScanned, 0, 1)
	var got api.ScanReport
	if err := json.Unmarshal(evs[0], &got); err != nil {
		t.Fatal(err)
	}
	if got.Trigger != scanStart || !slices.Equal(got.Added, rep.Added) {
		t.Fatalf("event %+v", got)
	}
}

func TestScanNeverEntersOmoHome(t *testing.T) {
	base := scanBase(t)
	l := scanHome(t, base)
	// The home and its worktrees lie below the root.
	gitInit(t, filepath.Join(l.Worktrees, "demo", "1"))
	gitInit(t, filepath.Join(l.Root, "repo"))
	gitInit(t, filepath.Join(base, "mine"))
	writeRoots(t, l, base)
	s := openScanServer(t, l, nil)
	rep := waitScanned(t, s, scanStart)
	if rep.Roots[0].Found != 1 || len(rep.Added) != 1 {
		t.Fatalf("report %+v", rep)
	}
	if _, ok := projectByPath(s, filepath.Join(base, "mine")); !ok {
		t.Fatal("the repository beside the home was not found")
	}
}

func TestNoAutomaticScanWithoutRoots(t *testing.T) {
	s := newTestServer(t)
	deadline := time.Now().Add(10 * time.Second)
	for s.scan.state().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	st := s.scan.state()
	if st.Running || st.Last != nil || s.scan.started() != 0 {
		t.Fatalf("state %+v", st)
	}
	// The button scans anyway and reports that there is nothing to scan.
	u := userCaller(t, s)
	rep := okJSON[api.ScanReport](t, u, 200, "POST", "/api/projects/rescan", map[string]any{})
	if rep.Trigger != scanButton || rep.Roots == nil || len(rep.Roots) != 0 || rep.Added == nil {
		t.Fatalf("report %+v", rep)
	}
}

func TestRescanRouteAddsNewRepos(t *testing.T) {
	base := scanBase(t)
	root := filepath.Join(base, "work")
	gitInit(t, filepath.Join(root, "first"))
	l := scanHome(t, base)
	writeRoots(t, l, root)
	s := openScanServer(t, l, nil)
	waitScanned(t, s, scanStart)
	gitInit(t, filepath.Join(root, "second"))
	u := userCaller(t, s)
	start := lastEventID(t, s)
	rep := okJSON[api.ScanReport](t, u, 200, "POST", "/api/projects/rescan", map[string]any{})
	if rep.Trigger != scanButton || len(rep.Added) != 1 || rep.Roots[0].Found != 2 {
		t.Fatalf("report %+v", rep)
	}
	p, ok := projectByPath(s, filepath.Join(root, "second"))
	if !ok || rep.Added[0] != p.ID {
		t.Fatalf("added %v, project %+v", rep.Added, p)
	}
	wantEvents(t, s, api.KindProjectsScanned, start, 1)
	if got := wantEvents(t, s, api.KindProjectChanged, start, 1); !strings.Contains(string(got[0]), p.ID) {
		t.Fatalf("project event %s", got[0])
	}
	rep = okJSON[api.ScanReport](t, u, 200, "POST", "/api/projects/rescan", map[string]any{})
	if len(rep.Added) != 0 || rep.Roots[0].Found != 2 {
		t.Fatalf("second report %+v", rep)
	}
	st := okJSON[api.ScanState](t, u, 200, "GET", "/api/projects/scan", nil)
	if st.Running || st.Last == nil || !st.Last.StartedAt.Equal(rep.StartedAt) {
		t.Fatalf("state %+v", st)
	}
	u.fails(400, api.CodeBadRequest, "POST", "/api/projects/rescan", map[string]any{"root": "x"})
}

func TestRescanRouteKeepsRenames(t *testing.T) {
	base := scanBase(t)
	root := filepath.Join(base, "work")
	gitInit(t, filepath.Join(root, "a", "one"))
	gitInit(t, filepath.Join(root, "b", "two"))
	l := scanHome(t, base)
	writeRoots(t, l, root)
	s := openScanServer(t, l, nil)
	waitScanned(t, s, scanStart)
	one, _ := projectByPath(s, filepath.Join(root, "a", "one"))
	two, _ := projectByPath(s, filepath.Join(root, "b", "two"))
	u := userCaller(t, s)
	okJSON[api.Project](t, u, 200, "PATCH", "/api/projects/"+one.ID, map[string]any{"name": "Renamed"})
	okJSON[api.Project](t, u, 200, "PATCH", "/api/projects/"+two.ID, map[string]any{"enabled": false})
	before := len(s.settings.Projects(true))
	rep := okJSON[api.ScanReport](t, u, 200, "POST", "/api/projects/rescan", map[string]any{})
	if len(rep.Added) != 0 || len(s.settings.Projects(true)) != before {
		t.Fatalf("report %+v", rep)
	}
	if p, _ := s.settings.Project(one.ID); p.Name != "Renamed" {
		t.Fatalf("rename lost: %+v", p)
	}
	if p, _ := s.settings.Project(two.ID); p.Enabled {
		t.Fatalf("switch lost: %+v", p)
	}
	// A missing project is counted and not added again when its folder
	// comes back.
	if err := os.Rename(filepath.Join(root, "b"), filepath.Join(base, "away")); err != nil {
		t.Fatal(err)
	}
	rep = okJSON[api.ScanReport](t, u, 200, "POST", "/api/projects/rescan", map[string]any{})
	if rep.Missing != 1 || len(rep.Added) != 0 {
		t.Fatalf("report %+v", rep)
	}
	if err := os.Rename(filepath.Join(base, "away"), filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	rep = okJSON[api.ScanReport](t, u, 200, "POST", "/api/projects/rescan", map[string]any{})
	if rep.Missing != 0 || len(rep.Added) != 0 || len(s.settings.Projects(true)) != before {
		t.Fatalf("report %+v", rep)
	}
}

func TestScanOnRootsChange(t *testing.T) {
	base := scanBase(t)
	root := filepath.Join(base, "work")
	gitInit(t, filepath.Join(root, "repo"))
	s := openScanServer(t, scanHome(t, base), nil)
	u := userCaller(t, s)
	g := s.settings.General()
	g.WorkspaceRoots = []settings.WorkspaceRoot{{Path: root, MaxDepth: 2, Ignore: []string{}}}
	okJSON[api.SettingsResponse](t, u, 200, "PUT", "/api/settings", g)
	rep := waitScanned(t, s, scanRootsChanged)
	if len(rep.Added) != 1 {
		t.Fatalf("report %+v", rep)
	}
	scans := s.scan.started()
	g = s.settings.General()
	g.Limits.Tasks++
	okJSON[api.SettingsResponse](t, u, 200, "PUT", "/api/settings", g)
	s.waitSettingsEvents(context.Background())
	if st := s.scan.state(); st.Running || s.scan.started() != scans {
		t.Fatalf("an unrelated change started a scan: %+v", st)
	}
	g.WorkspaceRoots[0].Ignore = []string{"repo"}
	okJSON[api.SettingsResponse](t, u, 200, "PUT", "/api/settings", g)
	s.waitSettingsEvents(context.Background())
	for s.scan.started() == scans || s.scan.state().Running {
		time.Sleep(10 * time.Millisecond)
	}
}

// blockingReadDir blocks the read of one folder until release is closed and
// counts the reads of that folder.
type blockingReadDir struct {
	dir     string
	armed   atomic.Bool
	reads   atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingReadDir(dir string) *blockingReadDir {
	return &blockingReadDir{dir: dir, entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingReadDir) read(dir string) ([]os.DirEntry, error) {
	if dir == b.dir {
		b.reads.Add(1)
		if b.armed.Load() {
			b.once.Do(func() { close(b.entered) })
			<-b.release
		}
	}
	return os.ReadDir(dir)
}

func TestScanOneAtATime(t *testing.T) {
	base := scanBase(t)
	root := filepath.Join(base, "work")
	gitInit(t, filepath.Join(root, "repo"))
	l := scanHome(t, base)
	writeRoots(t, l, root)
	block := newBlockingReadDir(root)
	s := openScanServer(t, l, &scanSeams{readDir: block.read})
	waitScanned(t, s, scanStart)
	u := userCaller(t, s)
	block.armed.Store(true)
	reads := block.reads.Load()
	scans := s.scan.started()
	reports := make(chan api.ScanReport, 2)
	post := func() {
		status, data := u.do("POST", "/api/projects/rescan", map[string]any{})
		var rep api.ScanReport
		if status != 200 || json.Unmarshal(data, &rep) != nil {
			t.Errorf("rescan: %d %s", status, data)
		}
		reports <- rep
	}
	go post()
	<-block.entered
	go post()
	for s.scan.waiting() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if st := okJSON[api.ScanState](t, u, 200, "GET", "/api/projects/scan", nil); !st.Running {
		t.Fatalf("state %+v", st)
	}
	close(block.release)
	a, b := <-reports, <-reports
	if !a.StartedAt.Equal(b.StartedAt) || a.Trigger != scanButton {
		t.Fatalf("reports %+v %+v", a, b)
	}
	if n := block.reads.Load() - reads; n != 1 {
		t.Fatalf("the root was read %d times, want once", n)
	}
	if n := s.scan.started() - scans; n != 1 {
		t.Fatalf("%d scans, want 1", n)
	}
}

func TestScanTimer(t *testing.T) {
	base := scanBase(t)
	root := filepath.Join(base, "work")
	gitInit(t, filepath.Join(root, "repo"))
	l := scanHome(t, base)
	writeRoots(t, l, root)
	var asked atomic.Int64
	s := openScanServer(t, l, &scanSeams{interval: func(d time.Duration) time.Duration {
		asked.Store(int64(d))
		return 20 * time.Millisecond
	}})
	rep := waitScanned(t, s, scanTimer)
	if len(rep.Roots) != 1 {
		t.Fatalf("report %+v", rep)
	}
	if time.Duration(asked.Load()) != time.Duration(s.settings.General().RescanInterval) {
		t.Fatalf("interval read %v", time.Duration(asked.Load()))
	}
}

func TestScanReservesTicketProjectIDs(t *testing.T) {
	base := scanBase(t)
	root := filepath.Join(base, "work")
	gitInit(t, filepath.Join(root, "demo"))
	l := scanHome(t, base)
	writeRoots(t, l, root)
	s := openScanServer(t, l, nil)
	waitScanned(t, s, scanStart)
	p, ok := projectByPath(s, filepath.Join(root, "demo"))
	if !ok || p.ID != "demo" {
		t.Fatalf("project %+v", p)
	}
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "t", Projects: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Cancel(ctx, board.User, int64(tk.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "demo"), filepath.Join(base, "away")); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.RemoveProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(base, "away"), filepath.Join(root, "demo")); err != nil {
		t.Fatal(err)
	}
	rep := okJSON[api.ScanReport](t, userCaller(t, s), 200, "POST", "/api/projects/rescan", map[string]any{})
	if !slices.Equal(rep.Added, []string{"demo-2"}) {
		t.Fatalf("added %v, want [demo-2]", rep.Added)
	}
}

func TestDiscoveryRoutesAreUserOnly(t *testing.T) {
	s := newTestServer(t)
	for _, role := range []api.Role{api.RoleDeveloper, api.RoleCEO, api.RolePM} {
		sess := liveSession(t, s, role)
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/api/projects/scan", nil},
			{"POST", "/api/projects/rescan", map[string]any{}},
			{"GET", "/api/folders", nil},
			{"GET", "/api/folders?path=" + t.TempDir(), nil},
		} {
			rec := socketDo(t, s, c.method, c.path, c.body, sess.ID)
			if rec.Code != http.StatusForbidden || errorCode(t, rec.Body) != api.CodeForbidden {
				t.Errorf("%s %s as %s: status %d", c.method, c.path, role, rec.Code)
			}
		}
	}
	if s.scan.started() != 0 {
		t.Fatal("an agent started a scan")
	}
	// The user may, on the socket too.
	rec := socketDo(t, s, "GET", "/api/projects/scan", nil, "")
	wantStatus(t, rec.Code, http.StatusOK)
}

// scannerGoroutines counts the goroutines running the scanner or a walk.
func scannerGoroutines() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "server.(*scanner)") || strings.Contains(g, "internal/discover.") || strings.Contains(g, "(*blockingReadDir).read") {
			count++
		}
	}
	return count
}

func TestCloseStopsScanner(t *testing.T) {
	base := scanBase(t)
	root := filepath.Join(base, "work")
	gitInit(t, filepath.Join(root, "repo"))
	l := scanHome(t, base)
	writeRoots(t, l, root)
	block := newBlockingReadDir(root)
	block.armed.Store(true)
	s := openScanServer(t, l, &scanSeams{readDir: block.read})
	<-block.entered
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	err := <-closed
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := projectByPath(s, filepath.Join(root, "repo")); ok {
		t.Fatal("a stopped scan added a project")
	}
	// The read that never answered is left to finish on its own; once it
	// does, nothing of the scan is left.
	close(block.release)
	for scannerGoroutines() != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	// A trigger after Close starts nothing.
	s.scan.request(scanButton)
	// Close joined the scanner; request only queues a signal and never
	// launches a worker of its own.
	if n := scannerGoroutines(); n != 0 {
		t.Fatalf("%d scanner goroutines after a trigger past Close", n)
	}
}

// TestScanStopsAtItsOverallLimit scans two roots while the first does not
// answer: the scan ends at its overall limit, the first root is cut and
// the second, not reached, is reported cut too, without being read.
func TestScanStopsAtItsOverallLimit(t *testing.T) {
	base := scanBase(t)
	slow, later := filepath.Join(base, "slow"), filepath.Join(base, "later")
	gitInit(t, filepath.Join(later, "repo"))
	if err := os.MkdirAll(slow, 0o700); err != nil {
		t.Fatal(err)
	}
	l := scanHome(t, base)
	writeRoots(t, l, slow, later)
	block := newBlockingReadDir(slow)
	block.armed.Store(true)
	t.Cleanup(func() { close(block.release) })
	var laterReads atomic.Int32
	read := func(dir string) ([]os.DirEntry, error) {
		if strings.HasPrefix(dir, later) {
			laterReads.Add(1)
		}
		return block.read(dir)
	}
	s := openScanServer(t, l, &scanSeams{readDir: read, limit: 300 * time.Millisecond})
	rep := waitScanned(t, s, scanStart)
	if len(rep.Roots) != 2 {
		t.Fatalf("roots %+v", rep.Roots)
	}
	if r := rep.Roots[0]; r.Path != slow || r.State != discover.StateCut {
		t.Errorf("first root %+v, want cut", r)
	}
	r := rep.Roots[1]
	if r.Path != later || r.State != discover.StateCut || !strings.Contains(r.Message, "not reached") || r.Added == nil || r.Skipped == nil {
		t.Errorf("second root %+v, want cut as not reached", r)
	}
	if n := laterReads.Load(); n != 0 {
		t.Errorf("the root not reached was read %d times", n)
	}
	if _, ok := projectByPath(s, filepath.Join(later, "repo")); ok {
		t.Error("a root not reached added a project")
	}
}
