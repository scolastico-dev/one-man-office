package server

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// diffEnv is a server with one project whose repository has a commit on main.
type diffEnv struct {
	t       *testing.T
	s       *Server
	u       *caller
	repo    string
	project string
}

func diffRepo(t *testing.T, s *Server) (repo, project string) {
	t.Helper()
	repo = committedRepo(t, "main")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "base.txt")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	p, err := s.settings.AddProject(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())
	return repo, p.ID
}

func newDiffEnv(t *testing.T) *diffEnv {
	t.Helper()
	s := newTestServer(t)
	repo, project := diffRepo(t, s)
	return &diffEnv{t: t, s: s, u: userCaller(t, s), repo: repo, project: project}
}

// cut makes branch from base in repo, checks it out in a new worktree and
// records both on ticket n; it returns the worktree.
func (e *diffEnv) cut(repo, project string, n int, branch, base string) string {
	e.t.Helper()
	wt := filepath.Join(e.t.TempDir(), "wt")
	gitIn(e.t, repo, "worktree", "add", "-q", "-b", branch, wt, base)
	if err := e.s.board.SetBranch(context.Background(), int64(n), project, branch, wt); err != nil {
		e.t.Fatal(err)
	}
	return wt
}

func writeIn(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitIn(t *testing.T, dir, name, content string) {
	t.Helper()
	writeIn(t, dir, name, content)
	gitIn(t, dir, "add", "--", name)
	gitIn(t, dir, "commit", "-q", "-m", "add "+strings.ToValidUTF8(name, "?"))
}

func (e *diffEnv) diff(c *caller, n int) api.TicketDiff {
	e.t.Helper()
	return okJSON[api.TicketDiff](e.t, c, 200, "GET", tp(n)+"/diff", nil)
}

func findFile(files []api.DiffFile, path string) *api.DiffFile {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}
	return nil
}

func TestDiffRouteStandaloneTask(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	commitIn(t, wt, "a.txt", "alpha\n")
	writeIn(t, wt, "base.txt", "one\ntwo\nthree\n") // uncommitted, tracked
	writeIn(t, wt, "u.txt", "secret contents\n")    // untracked

	d := e.diff(e.u, task.Number)
	if d.Ticket != task.Number || len(d.Projects) != 1 || d.ComputedAt.IsZero() {
		t.Fatalf("diff %+v", d)
	}
	p := d.Projects[0]
	if p.State != "ok" || p.Project != e.project || p.Base != "main" || p.Branch != "omo/1-work" || p.BaseCommit == "" || p.HeadCommit == "" {
		t.Fatalf("project %+v", p)
	}
	if len(p.Files) != 1 || p.Files[0].Path != "a.txt" || p.Files[0].Status != "added" || p.Files[0].Patch == nil || !strings.Contains(*p.Files[0].Patch, "+alpha") {
		t.Fatalf("files %+v", p.Files)
	}
	b := findFile(p.Uncommitted, "base.txt")
	if b == nil || b.Status != "modified" || b.Patch == nil || !strings.Contains(*b.Patch, "+three") {
		t.Fatalf("uncommitted %+v", p.Uncommitted)
	}
	u := findFile(p.Uncommitted, "u.txt")
	if u == nil || u.Status != "untracked" || u.Patch != nil {
		t.Fatalf("untracked %+v", p.Uncommitted)
	}
}

// An agent replaced its worktree folder by a link to another ticket's
// worktree: the diff routes do not read through the link.
func TestDiffRouteRefusesLinkedWorktree(t *testing.T) {
	e := newDiffEnv(t)
	one := newTask(t, e.u, e.project, "one")
	two := newTask(t, e.u, e.project, "two")
	wtOne := e.cut(e.repo, e.project, one.Number, "omo/1-one", "main")
	wtTwo := e.cut(e.repo, e.project, two.Number, "omo/2-two", "main")
	writeIn(t, wtTwo, "sibling-secret.txt", "secret\n")
	writeIn(t, wtTwo, "base.txt", "one\ntwo\nsibling\n")
	if err := os.Rename(wtOne, wtOne+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wtTwo, wtOne); err != nil {
		t.Fatal(err)
	}
	p := e.diff(e.u, one.Number).Projects[0]
	if len(p.Uncommitted) != 0 || !strings.Contains(p.Message, "could not be read") {
		t.Fatalf("uncommitted %+v message %q", p.Uncommitted, p.Message)
	}
	for _, path := range []string{"sibling-secret.txt", "base.txt"} {
		status, body := e.u.do("GET", fileURL(one.Number, e.project, path, "true"), nil)
		if status == 200 || strings.Contains(string(body), "sibling") {
			t.Errorf("%s: status %d body %s", path, status, body)
		}
	}
}

func TestDiffRouteTaskInStory(t *testing.T) {
	e := newDiffEnv(t)
	story := newStory(t, e.u, e.project)
	task := okJSON[api.Ticket](t, e.u, 201, "POST", "/api/tickets", api.CreateTicket{
		Type: api.TypeTask, Title: "part", Projects: []string{e.project}, Story: &story.Number})
	swt := e.cut(e.repo, e.project, story.Number, "omo/1-story", "main")
	twt := e.cut(e.repo, e.project, task.Number, "omo/2-part", "omo/1-story")
	commitIn(t, twt, "task.txt", "task\n")
	// A sibling merged into the integration branch after the task was cut.
	commitIn(t, swt, "sibling.txt", "sibling\n")

	p := e.diff(e.u, task.Number).Projects[0]
	if p.State != "ok" || p.Base != "omo/1-story" {
		t.Fatalf("project %+v", p)
	}
	if len(p.Files) != 1 || p.Files[0].Path != "task.txt" {
		t.Fatalf("files %+v", p.Files)
	}
	// The story itself is compared with its own base branch and shows both.
	sp := e.diff(e.u, story.Number).Projects[0]
	if sp.Base != "main" || len(sp.Files) != 1 || sp.Files[0].Path != "sibling.txt" {
		t.Fatalf("story %+v", sp)
	}
}

func TestDiffRouteStoryPerProject(t *testing.T) {
	e := newDiffEnv(t)
	repo2, project2 := diffRepo(t, e.s)
	story := okJSON[api.Ticket](t, e.u, 201, "POST", "/api/tickets", api.CreateTicket{
		Type: api.TypeStory, Title: "two", Projects: []string{e.project, project2}})
	wt := e.cut(e.repo, e.project, story.Number, "omo/1-two", "main")
	commitIn(t, wt, "x.txt", "x\n")
	_ = repo2

	d := e.diff(e.u, story.Number)
	if len(d.Projects) != 2 {
		t.Fatalf("projects %+v", d.Projects)
	}
	byID := map[string]api.ProjectDiff{}
	for _, p := range d.Projects {
		byID[p.Project] = p
	}
	if byID[e.project].State != "ok" || len(byID[e.project].Files) != 1 {
		t.Fatalf("first %+v", byID[e.project])
	}
	n := byID[project2]
	if n.State != "not_started" || n.Message == "" || n.Files == nil || n.Uncommitted == nil {
		t.Fatalf("second %+v", n)
	}
}

func TestDiffRouteBranchGone(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	commitIn(t, wt, "a.txt", "a\n")
	gitIn(t, e.repo, "worktree", "remove", "--force", wt)
	gitIn(t, e.repo, "branch", "-D", "omo/1-work")

	p := e.diff(e.u, task.Number).Projects[0]
	if p.State != "branch_gone" || !strings.Contains(p.Message, "omo/1-work") || len(p.Files) != 0 {
		t.Fatalf("project %+v", p)
	}
}

func TestDiffRouteStates(t *testing.T) {
	e := newDiffEnv(t)
	story := newStory(t, e.u, e.project)
	task := okJSON[api.Ticket](t, e.u, 201, "POST", "/api/tickets", api.CreateTicket{
		Type: api.TypeTask, Title: "part", Projects: []string{e.project}, Story: &story.Number})
	swt := e.cut(e.repo, e.project, story.Number, "omo/1-story", "main")
	e.cut(e.repo, e.project, task.Number, "omo/2-part", "omo/1-story")
	// The integration branch was removed: the base of the task is gone.
	gitIn(t, e.repo, "worktree", "remove", "--force", swt)
	gitIn(t, e.repo, "branch", "-D", "omo/1-story")
	p := e.diff(e.u, task.Number).Projects[0]
	if p.State != "branch_gone" || !strings.Contains(p.Message, "omo/1-story") || strings.Contains(p.Message, "omo/2-part") {
		t.Fatalf("base gone: %+v", p)
	}
	// The repository folder is gone: a state, with the worktree's changes unread.
	if err := os.RemoveAll(e.repo); err != nil {
		t.Fatal(err)
	}
	p = e.diff(e.u, task.Number).Projects[0]
	if p.State != "project_unavailable" || p.Message == "" {
		t.Fatalf("missing folder: %+v", p)
	}
}

func TestDiffRouteScope(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")

	dev := liveSession(t, e.s, api.RoleDeveloper) // works on a ticket of its own
	other := e.s.agent(t, dev.ID)
	for _, path := range []string{tp(task.Number) + "/diff", tp(task.Number) + "/diff/file?project=" + e.project + "&path=x", tp(999) + "/diff"} {
		other.fails(403, api.CodeForbidden, "GET", path, nil)
	}
	e.u.fails(404, api.CodeNotFound, "GET", tp(999)+"/diff", nil)
	e.u.fails(400, api.CodeBadRequest, "GET", "/api/tickets/x/diff", nil)
	// Its own ticket is readable for the agent.
	own := dev.Ticket
	okJSON[api.TicketDiff](t, other, 200, "GET", tp(*own)+"/diff", nil)
	// Unauthenticated callers learn nothing.
	for _, path := range []string{tp(task.Number) + "/diff", tp(999) + "/diff"} {
		resp := newBrowser(t, e.s).do("GET", path, nil, nil)
		wantStatus(t, resp.StatusCode, http.StatusUnauthorized)
	}
	// An unknown agent session reads nothing either.
	rec := socketDo(t, e.s, "GET", tp(task.Number)+"/diff", nil, "no-such-session")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown session: %d", rec.Code)
	}
}

func fileURL(n int, project, path string, uncommitted string) string {
	q := url.Values{"project": {project}, "path": {path}}
	if uncommitted != "" {
		q.Set("uncommitted", uncommitted)
	}
	return tp(n) + "/diff/file?" + q.Encode()
}

func TestDiffRouteFileOnlyListedPath(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	commitIn(t, wt, "a.txt", "alpha\n")
	writeIn(t, wt, "base.txt", "one\ntwo\nthree\n")
	n := task.Number

	f := okJSON[api.DiffFile](t, e.u, 200, "GET", fileURL(n, e.project, "a.txt", ""), nil)
	if f.Path != "a.txt" || f.Patch == nil || !strings.Contains(*f.Patch, "+alpha") {
		t.Fatalf("file %+v", f)
	}
	u := okJSON[api.DiffFile](t, e.u, 200, "GET", fileURL(n, e.project, "base.txt", "true"), nil)
	if u.Patch == nil || !strings.Contains(*u.Patch, "+three") {
		t.Fatalf("uncommitted %+v", u)
	}
	// A file that is only in the other list, a path outside the diff and a
	// path that is no file of the repository are all the same answer.
	for _, c := range []struct{ path, unc string }{
		{"base.txt", ""}, {"a.txt", "true"}, {"../../etc/passwd", ""}, {"/etc/passwd", ""}, {"../../etc/passwd", "true"},
		{":(glob)*", ""}, {"*", ""}, {"nothing.txt", ""}, {"a.txt\\x00", ""}, {"a.txt\\", ""},
	} {
		e.u.fails(404, api.CodeNotFound, "GET", fileURL(n, e.project, c.path, c.unc), nil)
	}
	e.u.fails(404, api.CodeNotFound, "GET", fileURL(n, "nope", "a.txt", ""), nil)
	// A project of the server that is not a project of this ticket.
	_, other := diffRepo(t, e.s)
	e.u.fails(404, api.CodeNotFound, "GET", fileURL(n, other, "a.txt", ""), nil)
	e.u.fails(400, api.CodeBadRequest, "GET", fileURL(n, e.project, "a.txt", "maybe"), nil)
	e.u.fails(400, api.CodeBadRequest, "GET", fileURL(n, e.project, "", ""), nil)
}

func TestEscapeDiffPath(t *testing.T) {
	for _, c := range []struct{ raw, shown string }{
		{"src/a b.go", "src/a b.go"},
		{"naïve/日本.txt", "naïve/日本.txt"},
		{"a\nb", `a\u{a}b`},
		{"a\tb", `a\u{9}b`},
		{"a\xffb", `a\xffb`},
		{"fdp‮exe.txt", `fdp\u{202e}exe.txt`},
		{`back\slash`, `back\\slash`},
		{"\x1b[31m", `\u{1b}[31m`},
		{"", ""},
	} {
		got := escapeDiffPath(c.raw)
		if got != c.shown {
			t.Errorf("escape(%q) = %q, want %q", c.raw, got, c.shown)
		}
		if !utf8.ValidString(got) || strings.ContainsFunc(got, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			t.Errorf("escape(%q) = %q is not safe", c.raw, got)
		}
		back, ok := unescapeDiffPath(got)
		if !ok || back != c.raw {
			t.Errorf("unescape(%q) = %q, %v; want %q", got, back, ok, c.raw)
		}
	}
	// Only the exact form escapeDiffPath produces is accepted.
	for _, bad := range []string{`a\u{41}`, `a\x41`, `a\n`, `a\`, `a\u{zz}`, `a\u{110000}`, `a\u{d800}`, `a\xf`, "a\nb", `a\xFF`, `a\u{0a}b`} {
		if raw, ok := unescapeDiffPath(bad); ok {
			t.Errorf("unescape(%q) accepted as %q", bad, raw)
		}
	}
}

func TestDiffRouteOddNames(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	odd := []string{"line\nbreak.txt", "bad\xffbyte.txt", "rlo‮exe.txt", "tab\there.txt", "-dash.txt", "plain.txt"}
	for _, name := range odd {
		writeIn(t, wt, name, "content of "+strings.ToValidUTF8(name, "?")+"\n")
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "odd")
	writeIn(t, wt, "base.txt", "changed\n")
	writeIn(t, wt, "bad\xffbyte.txt", "changed again\n")
	n := task.Number

	status, data := e.u.do("GET", tp(n)+"/diff", nil)
	if status != 200 {
		t.Fatalf("status %d: %s", status, data)
	}
	if !utf8.Valid(data) {
		t.Fatalf("the answer is not UTF-8: %q", data)
	}
	d := e.diff(e.u, n).Projects[0]
	if len(d.Files) != len(odd) {
		t.Fatalf("files %+v", d.Files)
	}
	for _, f := range d.Files {
		if !utf8.ValidString(f.Path) || strings.ContainsFunc(f.Path, func(r rune) bool { return r < 0x20 || r == 0x7f || r == 0x202e }) {
			t.Fatalf("unsafe path %q", f.Path)
		}
		// The displayed name addresses the file.
		got := okJSON[api.DiffFile](t, e.u, 200, "GET", fileURL(n, e.project, f.Path, ""), nil)
		if got.Path != f.Path || got.Patch == nil || !strings.Contains(*got.Patch, "content of") {
			t.Fatalf("round trip of %q: %+v", f.Path, got)
		}
	}
	if findFile(d.Files, `bad\xffbyte.txt`) == nil || findFile(d.Files, `line\u{a}break.txt`) == nil {
		t.Fatalf("names %+v", d.Files)
	}
	// Uncommitted list, same rule.
	u := findFile(d.Uncommitted, `bad\xffbyte.txt`)
	if u == nil {
		t.Fatalf("uncommitted %+v", d.Uncommitted)
	}
	got := okJSON[api.DiffFile](t, e.u, 200, "GET", fileURL(n, e.project, u.Path, "true"), nil)
	if got.Patch == nil || !strings.Contains(*got.Patch, "changed again") {
		t.Fatalf("uncommitted round trip %+v", got)
	}
	// Not the raw name.
	e.u.fails(404, api.CodeNotFound, "GET", fileURL(n, e.project, "bad\xffbyte.txt", ""), nil)
}

// slots blocks every diff in its slot until released.
type slotGate struct {
	entered atomic.Int32
	release chan struct{}
	seen    chan struct{}
	ctxEnd  atomic.Int32
}

func newSlotGate(s *Server) *slotGate {
	g := &slotGate{release: make(chan struct{}), seen: make(chan struct{}, 64)}
	s.diffHook = func(ctx context.Context) {
		g.entered.Add(1)
		g.seen <- struct{}{}
		select {
		case <-g.release:
		case <-ctx.Done():
			g.ctxEnd.Add(1)
		}
	}
	return g
}

func TestDiffRouteSemaphore(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	g := newSlotGate(e.s)
	path := tp(task.Number) + "/diff"

	b := newBrowser(t, e.s)
	var wg sync.WaitGroup
	results := make(chan int, 8)
	send := func(ctx context.Context) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := newRequest(t, "GET", b.srv.URL+path, nil).WithContext(ctx)
			req.Host = hostOmoLocalhost + ":" + b.port
			req.Header.Set("Authorization", "Bearer "+e.s.token)
			resp, err := b.srv.Client().Do(req)
			if err != nil {
				results <- -1
				return
			}
			resp.Body.Close()
			results <- resp.StatusCode
		}()
	}
	for range 4 {
		send(context.Background())
	}
	for range 4 {
		select {
		case <-g.seen:
		case <-time.After(10 * time.Second):
			t.Fatal("four requests did not start")
		}
	}
	fifthCtx, leave := context.WithCancel(context.Background())
	send(fifthCtx)
	time.Sleep(300 * time.Millisecond)
	if n := g.entered.Load(); n != 4 {
		t.Fatalf("%d diffs run at once, want 4", n)
	}
	leave() // the fifth client goes away while it waits
	if got := <-results; got != -1 {
		t.Fatalf("fifth result %d", got)
	}
	time.Sleep(200 * time.Millisecond)
	if n := g.entered.Load(); n != 4 {
		t.Fatalf("the fifth ran after its client left: %d", n)
	}
	close(g.release)
	for range 4 {
		if got := <-results; got != 200 {
			t.Fatalf("status %d", got)
		}
	}
	wg.Wait()
	// Every slot is free again.
	for range 6 {
		okJSON[api.TicketDiff](t, e.u, 200, "GET", path, nil)
	}
}

func TestDiffRouteClientLeavingStopsTheWork(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	g := newSlotGate(e.s)
	b := newBrowser(t, e.s)
	ctx, leave := context.WithCancel(context.Background())
	go func() {
		req := newRequest(t, "GET", b.srv.URL+tp(task.Number)+"/diff", nil).WithContext(ctx)
		req.Host = hostOmoLocalhost + ":" + b.port
		req.Header.Set("Authorization", "Bearer "+e.s.token)
		if resp, err := b.srv.Client().Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-g.seen:
	case <-time.After(10 * time.Second):
		t.Fatal("the diff did not start")
	}
	leave()
	deadline := time.Now().Add(10 * time.Second)
	for g.ctxEnd.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the work did not see the client leave")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDiffRoutesChangeNothing(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	commitIn(t, wt, "a.txt", "alpha\n")
	writeIn(t, wt, "base.txt", "changed\n")
	writeIn(t, wt, "u.txt", "new\n")

	snap := func() string {
		var sb strings.Builder
		for _, args := range [][]string{{"-C", wt, "status", "--porcelain=v1", "-z"}, {"-C", e.repo, "for-each-ref"}, {"-C", wt, "stash", "list"}} {
			cmd := exec.Command("git", args...)
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_OPTIONAL_LOCKS=0")
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			sb.Write(out)
		}
		gitdir, err := exec.Command("git", "-C", wt, "rev-parse", "--absolute-git-dir").Output()
		if err != nil {
			t.Fatal(err)
		}
		idx := filepath.Join(strings.TrimSpace(string(gitdir)), "index")
		data, err := os.ReadFile(idx)
		if err != nil {
			t.Fatal(err)
		}
		sb.WriteString(string(data))
		entries, _ := os.ReadDir(strings.TrimSpace(string(gitdir)))
		for _, en := range entries {
			sb.WriteString(en.Name() + "\n")
		}
		return sb.String()
	}
	getTicket := func() string {
		_, data := e.u.do("GET", tp(task.Number), nil)
		return string(data)
	}
	events := func() int {
		var n int
		if err := e.s.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM events`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	beforeGit, beforeTicket, beforeEvents := snap(), getTicket(), events()
	d := e.diff(e.u, task.Number)
	f := d.Projects[0].Files[0]
	okJSON[api.DiffFile](t, e.u, 200, "GET", fileURL(task.Number, e.project, f.Path, ""), nil)
	okJSON[api.DiffFile](t, e.u, 200, "GET", fileURL(task.Number, e.project, "base.txt", "true"), nil)
	if snap() != beforeGit {
		t.Fatal("the repository or the worktree changed")
	}
	if getTicket() != beforeTicket || events() != beforeEvents {
		t.Fatal("the board changed")
	}
}

// The server's git client refuses to let git look for a repository in the
// worktrees folder: commands there run only through resolved worktrees.
func TestServerGitKnowsTheAgentFolder(t *testing.T) {
	s := newTestServer(t)
	if !slices.Contains(s.git.AgentFolders(), filepath.Clean(s.opts.Layout.Worktrees)) {
		t.Fatalf("agent folders %v, want %s", s.git.AgentFolders(), s.opts.Layout.Worktrees)
	}
}
