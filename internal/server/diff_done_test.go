package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// closeWithCommits takes ticket n through merging to Done with the
// commits start..end recorded, as the engine's merge does.
func (e *diffEnv) closeWithCommits(n int, start, end string) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.s.db.Exec(`UPDATE tickets SET col = 'review', review_stage = 'merging', review_round = 1 WHERE id = ?`, n); err != nil {
		e.t.Fatal(err)
	}
	if start != "" {
		if err := e.s.board.RecordCommits(ctx, int64(n), e.project, start, end); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := e.s.board.CompleteMerge(ctx, int64(n)); err != nil {
		e.t.Fatal(err)
	}
}

// doneWork makes a task whose branch has a.txt and a change of base.txt
// while main moved on, closes it with its commits recorded and kept, then
// removes its worktree and branch and lets git collect garbage.
func doneWork(t *testing.T, e *diffEnv) (n int, start, end string) {
	t.Helper()
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	start = gitOut(t, e.repo, "rev-parse", "main")
	commitIn(t, wt, "a.txt", "alpha\n")
	commitIn(t, wt, "base.txt", "one\nTWO\n")
	end = gitOut(t, wt, "rev-parse", "HEAD")
	commitIn(t, e.repo, "main.txt", "main moved on\n")
	e.closeWithCommits(task.Number, start, end)
	if err := e.s.git.KeepCommits(e.repo, int64(task.Number), []string{start, end}); err != nil {
		t.Fatal(err)
	}
	gitIn(t, e.repo, "worktree", "remove", "--force", wt)
	gitIn(t, e.repo, "branch", "-D", "omo/1-work")
	// As the cleanup after a merge records it.
	if err := e.s.board.SetBranch(context.Background(), int64(task.Number), e.project, "", ""); err != nil {
		t.Fatal(err)
	}
	gitIn(t, e.repo, "reflog", "expire", "--expire=now", "--all")
	gitIn(t, e.repo, "gc", "-q", "--prune=now")
	return task.Number, start, end
}

func TestDiffRouteDoneTicketServesRecordedCommits(t *testing.T) {
	e := newDiffEnv(t)
	n, start, end := doneWork(t, e)

	tk := okJSON[api.Ticket](t, e.u, 200, "GET", tp(n), nil)
	if p := tk.Projects[0]; p.StartCommit != start || p.EndCommit != end {
		t.Fatalf("ticket answers %q..%q", p.StartCommit, p.EndCommit)
	}
	p := e.diff(e.u, n).Projects[0]
	if p.State != "ok" || !p.Recorded || p.BaseCommit != start || p.HeadCommit != end || p.RemoteBase != nil || p.Message != "" {
		t.Fatalf("project %+v", p)
	}
	var paths []string
	for _, f := range p.Files {
		paths = append(paths, f.Status+" "+f.Path)
	}
	if strings.Join(paths, ",") != "added a.txt,modified base.txt" {
		t.Fatalf("files %v", paths)
	}

	f := okJSON[api.DiffFile](t, e.u, 200, "GET", fileURL(n, e.project, "base.txt", ""), nil)
	if f.Patch == nil || !strings.Contains(*f.Patch, "+TWO") {
		t.Fatalf("file %+v", f)
	}
	f = okJSON[api.DiffFile](t, e.u, 200, "GET", fileURL(n, e.project, "base.txt", "")+"&context=all", nil)
	if f.Patch == nil || !strings.Contains(*f.Patch, " one") {
		t.Fatalf("file with context %+v", f)
	}
	// main's own change is no part of it, and a recorded diff has no
	// uncommitted changes.
	if code, body := e.u.do("GET", fileURL(n, e.project, "main.txt", ""), nil); code != http.StatusNotFound {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := e.u.do("GET", fileURL(n, e.project, "a.txt", "true"), nil); code != http.StatusNotFound {
		t.Fatalf("%d %s", code, body)
	}

	// The repository folder is gone: said plainly.
	if err := os.RemoveAll(e.repo); err != nil {
		t.Fatal(err)
	}
	p = e.diff(e.u, n).Projects[0]
	if p.State != "project_unavailable" || p.Message == "" {
		t.Fatalf("missing folder: %+v", p)
	}
}

func TestDiffRouteDoneTicketWithoutItsCommits(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	gone := strings.Repeat("e", 40)
	e.closeWithCommits(task.Number, gone, gone)
	p := e.diff(e.u, task.Number).Projects[0]
	if p.State != "commits_gone" || !p.Recorded || !strings.Contains(p.Message, "eeeeeeeeeeee..eeeeeeeeeeee") || len(p.Files) != 0 {
		t.Fatalf("project %+v", p)
	}
	if code, body := e.u.do("GET", fileURL(task.Number, e.project, "a.txt", ""), nil); code != http.StatusNotFound {
		t.Fatalf("%d %s", code, body)
	}
}

// A ticket done before omo recorded commits says so: with its branch
// still there, the branch is compared as it is now; without it, nothing
// can be shown.
func TestDiffRouteDoneTicketWithNothingRecorded(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	commitIn(t, wt, "a.txt", "alpha\n")
	if _, err := e.s.db.Exec(`UPDATE tickets SET col = 'done', resolution = 'completed', closed_at = '2026-11-01T10:00:00.000000000Z' WHERE id = ?`, task.Number); err != nil {
		t.Fatal(err)
	}
	p := e.diff(e.u, task.Number).Projects[0]
	if p.State != "ok" || p.Recorded || len(p.Files) != 1 || !strings.Contains(p.Message, "recorded no commits") {
		t.Fatalf("branch still there: %+v", p)
	}
	gitIn(t, e.repo, "worktree", "remove", "--force", wt)
	gitIn(t, e.repo, "branch", "-D", "omo/1-work")
	p = e.diff(e.u, task.Number).Projects[0]
	if p.State != "branch_gone" || !strings.Contains(p.Message, "recorded no commits") {
		t.Fatalf("branch gone: %+v", p)
	}
	// omo forgets the branch it deleted after a merge.
	if err := e.s.board.SetBranch(context.Background(), int64(task.Number), e.project, "", ""); err != nil {
		t.Fatal(err)
	}
	p = e.diff(e.u, task.Number).Projects[0]
	if p.State != "branch_gone" || !strings.Contains(p.Message, "recorded no commits") {
		t.Fatalf("branch forgotten: %+v", p)
	}
}
