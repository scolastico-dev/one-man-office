package gitops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// bareRemote creates an empty bare repository and returns its folder.
func bareRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	tg(t, filepath.Dir(dir), "init", "-q", "--bare", dir)
	return dir
}

// remoteRef returns the commit refs/heads/<branch> has in the bare
// repository remote, or "".
func remoteRef(t *testing.T, remote, branch string) string {
	t.Helper()
	out := tg(t, remote, "for-each-ref", "--format=%(objectname)", "refs/heads/"+branch)
	return strings.TrimSpace(out)
}

func TestRemoteForOrder(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	ctx := context.Background()
	for _, n := range []string{"origin", "up", "def", "push"} {
		tg(t, repo, "remote", "add", n, "https://example.com/"+n+".git")
	}
	want := func(name string) {
		t.Helper()
		r, err := g.RemoteFor(ctx, repo, "main")
		if err != nil {
			t.Fatal(err)
		}
		if r.Name != name || r.URL != "https://example.com/"+name+".git" {
			t.Fatalf("RemoteFor = %+v, want %s", r, name)
		}
	}
	want("origin")
	tg(t, repo, "config", "branch.main.remote", "up")
	want("up")
	tg(t, repo, "config", "remote.pushDefault", "def")
	want("def")
	tg(t, repo, "config", "branch.main.pushRemote", "push")
	want("push")
	// pushurl wins over url.
	tg(t, repo, "config", "remote.push.pushurl", "git@example.com:o/p.git")
	r, err := g.RemoteFor(ctx, repo, "main")
	if err != nil || r.URL != "git@example.com:o/p.git" {
		t.Fatalf("RemoteFor = %+v, %v; want the pushurl", r, err)
	}
	// Another branch falls back to remote.pushDefault.
	if r, err := g.RemoteFor(ctx, repo, "dev"); err != nil || r.Name != "def" {
		t.Fatalf("RemoteFor(dev) = %+v, %v; want def", r, err)
	}
}

func TestRemoteForRefusesOptionNames(t *testing.T) {
	g := testGit(t)
	ctx := context.Background()
	cases := []struct{ key, value string }{
		{"branch.main.pushRemote", "-x"},
		{"branch.main.pushRemote", "--receive-pack=touch x"},
		{"remote.pushDefault", "a b"},
		{"remote.origin.url", "ext::sh -c x"},
		{"remote.origin.url", "-oProxyCommand=x"},
		{"remote.origin.url", "file:///x"},
		{"remote.origin.url", "http://user:SECRET@example.com/x"},
		{"remote.origin.url", "../relative"},
	}
	for _, c := range cases {
		repo := newTestRepo(t)
		tg(t, repo, "remote", "add", "origin", "https://example.com/o.git")
		if strings.HasPrefix(c.key, "remote.origin.url") {
			tg(t, repo, "config", "--unset-all", "remote.origin.url")
		}
		tg(t, repo, "config", "--", c.key, c.value)
		_, err := g.RemoteFor(ctx, repo, "main")
		if !errors.Is(err, ErrBadRemote) {
			t.Errorf("%s=%q: err = %v, want ErrBadRemote", c.key, c.value, err)
		}
		if err != nil && strings.Contains(err.Error(), "SECRET") {
			t.Errorf("the refusal repeats the password: %v", err)
		}
	}
	repo := newTestRepo(t)
	for _, base := range []string{"", "-x", "a..b", "main\n[x]"} {
		if _, err := g.RemoteFor(ctx, repo, base); !errors.Is(err, ErrBadRemote) {
			t.Errorf("base %q: err = %v, want ErrBadRemote", base, err)
		}
	}
	if _, err := g.RemoteFor(ctx, repo, "main"); !errors.Is(err, ErrBadRemote) {
		t.Errorf("no remote at all: err = %v, want ErrBadRemote", err)
	}
}

func TestRemoteForIgnoresIncludes(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	tg(t, repo, "remote", "add", "origin", "https://example.com/o.git")
	inc := filepath.Join(t.TempDir(), "inc")
	write(t, inc, "[remote \"origin\"]\n\tpushurl = https://evil.example/x.git\n[branch \"main\"]\n\tpushRemote = evil\n")
	tg(t, repo, "config", "include.path", inc)
	tg(t, repo, "config", "includeIf.gitdir:/.path", inc)
	r, err := g.RemoteFor(context.Background(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if r != (Remote{Name: "origin", URL: "https://example.com/o.git"}) {
		t.Fatalf("RemoteFor = %+v: an included file was read", r)
	}
}

func TestRemoteForRefusesAgentFolder(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	g.SetAgentFolders(filepath.Dir(repo))
	if _, err := g.RemoteFor(context.Background(), repo, "main"); !errors.Is(err, ErrAgentFolder) {
		t.Fatalf("err = %v, want ErrAgentFolder", err)
	}
	if err := g.PushCommit(context.Background(), repo, Remote{Name: "origin", URL: bareRemote(t)}, strings.Repeat("a", 40), "omo/1-x", ""); !errors.Is(err, ErrAgentFolder) {
		t.Fatalf("PushCommit: err = %v, want ErrAgentFolder", err)
	}
}

func TestPushCommitPushesExactCommit(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	tg(t, repo, "remote", "add", "origin", remote)
	pinned := commitOn(t, repo, "omo/1-x", "reviewed", map[string]string{"f.txt": "reviewed\n"})
	commitOn(t, repo, "omo/1-x", "after review", map[string]string{"f.txt": "later\n"})
	r, err := g.RemoteFor(context.Background(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.PushCommit(context.Background(), repo, r, pinned, "omo/1-x", ""); err != nil {
		t.Fatal(err)
	}
	if got := remoteRef(t, remote, "omo/1-x"); got != pinned {
		t.Fatalf("remote has %q, want the pinned %s", got, pinned)
	}
	if out := strings.TrimSpace(refs(t, remote)); strings.Count(out, "\n") != 0 {
		t.Errorf("the remote got more than one ref:\n%s", out)
	}
	// Pushing it again is fine.
	if err := g.PushCommit(context.Background(), repo, r, pinned, "omo/1-x", ""); err != nil {
		t.Fatal(err)
	}
}

func TestPushCommitNeverForces(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	first := commitOn(t, repo, "omo/1-x", "one", map[string]string{"f.txt": "one\n"})
	second := commitOn(t, repo, "omo/1-x", "two", map[string]string{"f.txt": "two\n"})
	r := Remote{Name: "origin", URL: remote}
	ctx := context.Background()
	if err := g.PushCommit(ctx, repo, r, second, "omo/1-x", ""); err != nil {
		t.Fatal(err)
	}
	// The repository asks for forced pushes everywhere; omo still does not.
	tg(t, repo, "config", "remote.origin.url", remote)
	tg(t, repo, "config", "remote.origin.push", "+refs/heads/*:refs/heads/*")
	tg(t, repo, "config", "remote.origin.mirror", "true")
	err := g.PushCommit(ctx, repo, r, first, "omo/1-x", second)
	if !errors.Is(err, ErrPushRejected) {
		t.Fatalf("err = %v, want ErrPushRejected", err)
	}
	if got := remoteRef(t, remote, "omo/1-x"); got != second {
		t.Fatalf("remote moved to %s", got)
	}
}

func TestPushCommitRefusesBadInput(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	commit := rev(t, repo, "HEAD")
	r := Remote{Name: "origin", URL: remote}
	ctx := context.Background()
	for _, branch := range []string{
		"main", "omo/", "-x", "--force", "omo/-x/../y", "+omo/x", "omo/a:refs/heads/main", "omo/x^", "omo/x~1",
		"omo/a b", "omo/a..b", "omo/@{-1}", "omo/a‮b", "omo/a​b", "omo/a\nb", "omo/x.lock", "omo/*",
	} {
		if err := g.PushCommit(ctx, repo, r, commit, branch, ""); !errors.Is(err, ErrBadRef) {
			t.Errorf("branch %q: err = %v, want ErrBadRef", branch, err)
		}
	}
	for _, c := range []string{"", "HEAD", "main", "--all", commit[:12], strings.ToUpper(commit), commit + ":refs/heads/main"} {
		if err := g.PushCommit(ctx, repo, r, c, "omo/1-x", ""); err == nil {
			t.Errorf("commit %q was accepted", c)
		}
	}
	for _, bad := range []Remote{
		{Name: "-x", URL: remote},
		{Name: "origin", URL: "ext::sh -c x"},
		{Name: "origin", URL: "--receive-pack=x"},
		{Name: "origin", URL: "http://h/x"},
	} {
		if err := g.PushCommit(ctx, repo, bad, commit, "omo/1-x", ""); !errors.Is(err, ErrBadRemote) {
			t.Errorf("remote %+v: err = %v, want ErrBadRemote", bad, err)
		}
	}
	if out := strings.TrimSpace(refs(t, remote)); out != "" {
		t.Errorf("the remote got refs: %s", out)
	}
}

func TestPushCommitIgnoresRepositoryRedirects(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	elsewhere := bareRemote(t)
	commit := commitOn(t, repo, "omo/1-x", "x", map[string]string{"f.txt": "x\n"})
	tg(t, repo, "remote", "add", "origin", remote)
	r, err := g.RemoteFor(context.Background(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	// Planted after the remote was read: a push URL, rewrites of the
	// address, refspecs and push.default that would send other refs.
	for _, kv := range [][2]string{
		{"url." + elsewhere + ".pushInsteadOf", remote},
		{"url." + elsewhere + ".insteadOf", remote},
		{"remote.origin.pushurl", elsewhere},
		{"remote.origin.push", "refs/heads/main:refs/heads/omo/1-x"},
		{"remote.origin.mirror", "true"},
		{"push.default", "matching"},
		{"push.followTags", "true"},
		{"alias.push", "!touch planted"},
	} {
		tg(t, repo, "config", "--add", kv[0], kv[1])
	}
	tg(t, repo, "tag", "-a", "t1", "-m", "tag")
	if err := g.PushCommit(context.Background(), repo, r, commit, "omo/1-x", ""); err != nil {
		t.Fatal(err)
	}
	if got := remoteRef(t, remote, "omo/1-x"); got != commit {
		t.Fatalf("remote has %q, want %s", got, commit)
	}
	if out := strings.TrimSpace(refs(t, remote)); strings.Contains(out, "\n") {
		t.Errorf("the remote got more than one ref:\n%s", out)
	}
	if out := strings.TrimSpace(refs(t, elsewhere)); out != "" {
		t.Errorf("the push was redirected:\n%s", out)
	}
}

func TestPushCommitHidesCredentials(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	commit := rev(t, repo, "HEAD")
	r := Remote{Name: "origin", URL: "https://user:SECRET@127.0.0.1:1/x.git"}
	if a := r.Address(); strings.Contains(a, "SECRET") || strings.Contains(a, "user") {
		t.Errorf("Address() = %q", a)
	}
	err := g.PushCommit(context.Background(), repo, r, commit, "omo/1-x", "")
	if err == nil {
		t.Fatal("pushing to a closed port succeeded")
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "user:") {
		t.Fatalf("the error repeats the credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "https://127.0.0.1:1/x.git") {
		t.Errorf("the error does not name the address: %v", err)
	}
}

// Any name the branch template can give is pushed; main and master never
// are, whatever the caller says.
func TestPushCommitAcceptsAnyTemplateName(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	tg(t, repo, "remote", "add", "origin", remote)
	r, err := g.RemoteFor(context.Background(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"omo/fix-login", "omo/12-fix-login", "fix-login", "jb/task/fix-login", "JIRA-12_fix"} {
		commit := commitOn(t, repo, branch, "work", map[string]string{"f.txt": branch})
		if err := g.PushCommit(context.Background(), repo, r, commit, branch, ""); err != nil {
			t.Errorf("%s: %v", branch, err)
		} else if got := remoteRef(t, remote, branch); got != commit {
			t.Errorf("%s: remote has %q", branch, got)
		}
	}
	commit := rev(t, repo, "fix-login")
	for _, branch := range []string{"main", "master", "HEAD", "refs/heads/x"} {
		if err := g.PushCommit(context.Background(), repo, r, commit, branch, ""); !errors.Is(err, ErrBadRef) {
			t.Errorf("%s: %v", branch, err)
		}
	}
	if got := remoteRef(t, remote, "main"); got != "" {
		t.Errorf("main was pushed: %s", got)
	}
}

// A branch of the remote that omo did not push is left alone: neither moved
// forward nor taken over, even when the push would be a fast-forward; the
// remote's default branch is refused under any name. A branch at the commit
// omo pushed before may move on, and pushing the same commit again is fine.
func TestPushCommitLeavesForeignRemoteBranchesAlone(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	ctx := context.Background()
	r := Remote{Name: "origin", URL: remote}
	base := rev(t, repo, "main")
	tg(t, repo, "push", "-q", remote, "main:refs/heads/feature", "main:refs/heads/trunk")
	tg(t, remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
	next := commitOn(t, repo, "feature", "mine", map[string]string{"f.txt": "mine\n"})
	err := g.PushCommit(ctx, repo, r, next, "feature", "")
	if !errors.Is(err, ErrForeignBranch) || !strings.Contains(err.Error(), base) {
		t.Fatalf("push onto a foreign branch: %v", err)
	}
	if got := remoteRef(t, remote, "feature"); got != base {
		t.Fatalf("the foreign branch moved to %s", got)
	}
	if err := g.PushCommit(ctx, repo, r, next, "trunk", base); !errors.Is(err, ErrBadRef) || !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("push to the default branch: %v", err)
	}
	// What omo pushed before may move on.
	if err := g.PushCommit(ctx, repo, r, next, "feature", base); err != nil {
		t.Fatal(err)
	}
	if err := g.PushCommit(ctx, repo, r, next, "feature", ""); err != nil {
		t.Fatalf("the same commit again: %v", err)
	}
	if got := remoteRef(t, remote, "feature"); got != next {
		t.Fatalf("feature is at %s", got)
	}
}
