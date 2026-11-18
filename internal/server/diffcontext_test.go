package server

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// lines is n lines "line 1" … "line n", the lines in changed with " changed".
func lines(n int, changed ...int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "line %d", i)
		for _, c := range changed {
			if c == i {
				sb.WriteString(" changed")
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func contextURL(n int, project, path, uncommitted, context string) string {
	return fileURL(n, project, path, uncommitted) + "&context=" + url.QueryEscape(context)
}

// hunkHeads returns the "@@ … @@" lines of a patch.
func hunkHeads(p *string) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(*p, "\n") {
		if strings.HasPrefix(l, "@@ ") {
			out = append(out, l[:strings.Index(l[3:], " @@")+6])
		}
	}
	return out
}

func TestDiffRouteFileContext(t *testing.T) {
	e := newDiffEnv(t)
	commitIn(t, e.repo, "long.txt", lines(100))
	commitIn(t, e.repo, "gone.txt", lines(20))
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	writeIn(t, wt, "long.txt", lines(100, 10, 90))
	gitIn(t, wt, "rm", "-q", "gone.txt")
	gitIn(t, wt, "mv", "base.txt", "moved.txt")
	gitIn(t, wt, "commit", "-q", "-a", "-m", "changes")
	writeIn(t, wt, "long.txt", lines(100, 10, 50, 90))
	n := task.Number

	get := func(path, unc, context string) api.DiffFile {
		t.Helper()
		return okJSON[api.DiffFile](t, e.u, 200, "GET", contextURL(n, e.project, path, unc, context), nil)
	}
	if h := hunkHeads(get("long.txt", "", "3").Patch); strings.Join(h, " ") != "@@ -7,7 +7,7 @@ @@ -87,7 +87,7 @@" {
		t.Fatalf("three lines: %v", h)
	}
	if h := hunkHeads(get("long.txt", "", "30").Patch); strings.Join(h, " ") != "@@ -1,40 +1,40 @@ @@ -60,41 +60,41 @@" {
		t.Fatalf("thirty lines: %v", h)
	}
	whole := get("long.txt", "", "all")
	if h := hunkHeads(whole.Patch); strings.Join(h, "") != "@@ -1,100 +1,100 @@" || whole.Truncated || whole.Additions != 2 {
		t.Fatalf("whole: %v %+v", h, whole)
	}
	if h := hunkHeads(get("long.txt", "", "1048576").Patch); strings.Join(h, "") != "@@ -1,100 +1,100 @@" {
		t.Fatalf("the most lines: %v", h)
	}
	// Uncommitted: the worktree against its HEAD.
	if h := hunkHeads(get("long.txt", "true", "all").Patch); strings.Join(h, "") != "@@ -1,100 +1,100 @@" {
		t.Fatalf("uncommitted whole: %v", h)
	}
	// A renamed and a deleted file.
	if f := get("moved.txt", "", "all"); f.OldPath != "base.txt" || f.Patch == nil {
		t.Fatalf("renamed: %+v", f)
	}
	if f := get("gone.txt", "", "all"); f.Status != "deleted" || strings.Join(hunkHeads(f.Patch), "") != "@@ -1,20 +0,0 @@" {
		t.Fatalf("deleted: %+v", f)
	}

	// Junk values are refused; a path outside the diff is not found.
	for _, c := range []string{"", "-1", "1048577", "x", "3.5", "ALL", "1e3", " 3"} {
		e.u.fails(400, api.CodeBadRequest, "GET", contextURL(n, e.project, "long.txt", "", c), nil)
	}
	for _, c := range []struct{ path, unc string }{{"README.md", ""}, {"../../etc/passwd", ""}, {"base.txt", ""}, {"moved.txt", "true"}, {":(glob)*", "true"}} {
		e.u.fails(404, api.CodeNotFound, "GET", contextURL(n, e.project, c.path, c.unc, "all"), nil)
	}
	// The access rule of the diff.
	other := e.s.agent(t, liveSession(t, e.s, api.RoleDeveloper).ID)
	other.fails(403, api.CodeForbidden, "GET", contextURL(n, e.project, "long.txt", "", "all"), nil)
}

func TestDiffRouteFileContextTooLarge(t *testing.T) {
	e := newDiffEnv(t)
	var before, after strings.Builder
	for i := 0; i < 40000; i++ {
		l := fmt.Sprintf("line %07d of a file larger than the limit\n", i)
		before.WriteString(l)
		if i == 100 {
			after.WriteString("changed\n")
		} else {
			after.WriteString(l)
		}
	}
	commitIn(t, e.repo, "big.txt", before.String())
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	commitIn(t, wt, "big.txt", after.String())
	writeIn(t, wt, "big.txt", after.String()+"more\n")
	n := task.Number

	if f := okJSON[api.DiffFile](t, e.u, 200, "GET", contextURL(n, e.project, "big.txt", "", "20"), nil); f.Patch == nil || f.Truncated {
		t.Fatalf("twenty lines: %+v", f.Truncated)
	}
	for _, unc := range []string{"", "true"} {
		status, body := e.u.do("GET", contextURL(n, e.project, "big.txt", unc, "all"), nil)
		if status != 422 || !strings.Contains(string(body), `"code":"validation"`) || !strings.Contains(string(body), "1 MiB") {
			t.Fatalf("whole big file (uncommitted=%q): %d %s", unc, status, body)
		}
	}
	if gitops.MaxSingleFilePatch != 1<<20 {
		t.Fatal("the message names 1 MiB")
	}
}

func TestDiffRouteFileContextRemoteBaseAndLinks(t *testing.T) {
	e := newDiffEnv(t)
	commitIn(t, e.repo, "long.txt", lines(50))
	e.behindOrigin()
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "origin/main")
	commitIn(t, wt, "long.txt", lines(50, 25))
	n := task.Number

	remote := contextURL(n, e.project, "long.txt", "", "all") + "&base=remote"
	if f := okJSON[api.DiffFile](t, e.u, 200, "GET", remote, nil); strings.Join(hunkHeads(f.Patch), "") != "@@ -1,50 +1,50 @@" {
		t.Fatalf("against origin/main: %+v", f)
	}
	e.u.fails(404, api.CodeNotFound, "GET", contextURL(n, e.project, "up0.txt", "", "all")+"&base=remote", nil)

	// A link in the worktree is shown as the link, never followed.
	secret := filepath.Join(t.TempDir(), "secret.txt")
	writeIn(t, filepath.Dir(secret), "secret.txt", "SECRET CONTENT\n")
	if err := os.Symlink(secret, filepath.Join(wt, "link")); err != nil {
		t.Skip("no links here:", err)
	}
	gitIn(t, wt, "add", "link")
	f := okJSON[api.DiffFile](t, e.u, 200, "GET", contextURL(n, e.project, "link", "true", "all"), nil)
	if f.Patch == nil || strings.Contains(*f.Patch, "SECRET") {
		t.Fatalf("link: %+v", f)
	}
	// The patch of a link is its target as git stored it.
	want := "+" + secret
	if !strings.Contains(*f.Patch, want) {
		t.Fatalf("link: patch %q, want %q in it", *f.Patch, want)
	}
}

// A worktree whose .git the agent replaced by a repository of its own: no
// command of that repository runs, and nothing of it is shown.
func TestDiffRouteFileContextHostileDotGit(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	writeIn(t, wt, "base.txt", "one\ntwo\nthree\n")
	marker := filepath.Join(t.TempDir(), "ran")
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "init", "-q", "-b", "main")
	gitIn(t, wt, "config", "filter.x.clean", "touch '"+marker+"'; cat")
	gitIn(t, wt, "config", "diff.external", "touch '"+marker+"'; true #")
	writeIn(t, wt, ".gitattributes", "* filter=x\n")
	status, _ := e.u.do("GET", contextURL(task.Number, e.project, "base.txt", "true", "all"), nil)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a command of the planted repository ran")
	}
	if status == 200 {
		// The repository's own record still names the worktree: the diff is
		// the worktree against the branch, read without the planted config.
		f := okJSON[api.DiffFile](t, e.u, 200, "GET", contextURL(task.Number, e.project, "base.txt", "true", "all"), nil)
		if f.Patch == nil || !strings.Contains(*f.Patch, "+three") {
			t.Fatalf("patch %+v", f)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a command of the planted repository ran")
	}
}
