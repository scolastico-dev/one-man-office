package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchTargetUsesRecordedURLAndPreservesCheckout(t *testing.T) {
	g := testGit(t)
	source := newTestRepo(t)
	remote := bareRemote(t)
	tg(t, source, "push", remote, "main", "main:other")
	want := commitOn(t, source, "main", "remote advancement", map[string]string{"remote.txt": "new\n"})
	tg(t, source, "tag", "remote-tag")
	tg(t, source, "push", remote, "main", "--tags")
	repo := newTestRepo(t)
	// The name now points elsewhere; only the saved address is authoritative.
	tg(t, repo, "remote", "add", "origin", bareRemote(t))
	tg(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	write(t, filepath.Join(repo, ".git", "FETCH_HEAD"), "previous fetch\n")
	write(t, filepath.Join(repo, "f.txt"), "staged user work\n")
	tg(t, repo, "add", "f.txt")
	write(t, filepath.Join(repo, "f.txt"), "unstaged user work\n")
	before := snapshot(t, repo)
	index := read(t, filepath.Join(repo, ".git", "index"))
	userRefs := tg(t, repo, "for-each-ref", "refs/heads/", "refs/remotes/", "refs/tags/")
	trace := filepath.Join(t.TempDir(), "trace")
	g.SetEnvironment(isolatedEnv(t, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=false", "GIT_TRACE="+trace))
	got, err := g.FetchTarget(context.Background(), repo, Remote{Name: "origin", URL: remote}, "main")
	if err != nil || got != want {
		t.Fatalf("FetchTarget = %q, %v; want %s", got, err, want)
	}
	if got := rev(t, repo, want+"^{commit}"); got != want {
		t.Fatalf("fetched objects are inaccessible: %s", got)
	}
	sameSnapshot(t, "fetch", before, snapshot(t, repo))
	if read(t, filepath.Join(repo, ".git", "index")) != index {
		t.Error("fetch rewrote the index")
	}
	if read(t, filepath.Join(repo, ".git", "FETCH_HEAD")) != "previous fetch\n" {
		t.Error("fetch rewrote FETCH_HEAD")
	}
	if after := tg(t, repo, "for-each-ref", "refs/heads/", "refs/remotes/", "refs/tags/"); after != userRefs {
		t.Errorf("fetch changed user refs: %s", after)
	}
	if got := strings.TrimSpace(tg(t, repo, "for-each-ref", "--format=%(objectname)", "refs/omo/export-target/")); got != want {
		t.Fatalf("target was not retained under an omo ref: %q", got)
	}
	if n := strings.Count(read(t, trace), " fetch --"); n != 1 {
		t.Fatalf("fetch invocation count = %d, want 1; trace:\n%s", n, read(t, trace))
	}
	noLeftovers(t, repo)
}

func TestFetchTargetMissingTargetAndFailure(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	source := newTestRepo(t)
	tg(t, source, "push", remote, "main")
	r := Remote{Name: "origin", URL: remote}
	if _, err := g.FetchTarget(context.Background(), repo, r, "main"); err != nil {
		t.Fatal(err)
	}
	tg(t, remote, "update-ref", "-d", "refs/heads/main")
	for _, c := range []struct {
		remote Remote
		base   string
	}{
		{r, "main"}, // A retained target must not conceal deletion remotely.
		{r, "missing"},
		{Remote{Name: "origin", URL: filepath.Join(t.TempDir(), "missing.git")}, "main"},
	} {
		if id, err := g.FetchTarget(context.Background(), repo, c.remote, c.base); err == nil || id != "" {
			t.Errorf("FetchTarget(%+v, %q) = %q, %v; want an error", c.remote, c.base, id, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.FetchTarget(ctx, repo, r, "main"); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled fetch error = %v", err)
	}
}

func TestFetchTargetRejectsInvalidRemoteAndBase(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	for _, c := range []struct {
		remote Remote
		base   string
	}{
		{Remote{Name: "-option", URL: bareRemote(t)}, "main"},
		{Remote{Name: "origin", URL: "ext::false"}, "main"},
		{Remote{Name: "origin", URL: "https://user:SECRET@example.com/repo"}, "main~1"},
		{Remote{Name: "origin", URL: bareRemote(t)}, ""},
	} {
		if _, err := g.FetchTarget(context.Background(), repo, c.remote, c.base); err == nil {
			t.Errorf("accepted invalid remote/base: %+v %q", c.remote, c.base)
		} else if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("error exposed credentials: %v", err)
		}
	}
}

func TestExportContainedRecognizesIntegrationStyles(t *testing.T) {
	for _, style := range []string{"merge", "fast-forward", "rebase", "squash", "unchanged", "partial", "conflict"} {
		t.Run(style, func(t *testing.T) {
			g := testGit(t)
			repo := newTestRepo(t)
			start := rev(t, repo, "HEAD")
			first := commitOn(t, repo, "exported", "change", map[string]string{"f.txt": "exported\n"})
			commit := commitOn(t, repo, "exported", "add", map[string]string{"added.txt": "exported addition\n"})
			want := false
			switch style {
			case "fast-forward":
				tg(t, repo, "merge", "--ff-only", "exported")
				want = true
			case "merge", "rebase", "squash":
				commitOn(t, repo, "main", "base advancement", map[string]string{"other.txt": "advanced\n"})
				switch style {
				case "merge":
					tg(t, repo, "merge", "--no-ff", "-m", "merge export", "exported")
				case "rebase":
					tg(t, repo, "cherry-pick", first, commit)
				case "squash":
					tg(t, repo, "merge", "--squash", "exported")
					tg(t, repo, "commit", "-m", "squash export")
				}
				want = true
			case "partial":
				commitOn(t, repo, "main", "partial", map[string]string{"f.txt": "exported\n"})
			case "conflict":
				commitOn(t, repo, "main", "different", map[string]string{"f.txt": "different\n"})
			}
			if want {
				commitOn(t, repo, "main", "later unrelated advancement", map[string]string{"later.txt": "later\n"})
			}
			target := rev(t, repo, "HEAD")
			// Detection relies on the retained review commit, never the export branch.
			if err := g.KeepCommits(repo, 1, []string{start, commit}); err != nil {
				t.Fatal(err)
			}
			tg(t, repo, "branch", "-D", "exported")
			before := snapshot(t, repo)
			beforeRefs := refs(t, repo)
			got, err := g.ExportContained(repo, start, commit, target)
			if err != nil || got != want {
				t.Fatalf("ExportContained = %v, %v; want %v", got, err, want)
			}
			sameSnapshot(t, "detection", before, snapshot(t, repo))
			if refs(t, repo) != beforeRefs {
				t.Error("detection changed refs")
			}
			noLeftovers(t, repo)
		})
	}
}

func TestExportContainedRejectsEmptyAndMissingCommits(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	start := rev(t, repo, "HEAD")
	tg(t, repo, "commit", "--allow-empty", "-m", "empty export")
	commit := rev(t, repo, "HEAD")
	for _, c := range []struct{ start, commit, target string }{
		{start, start, commit},
		{start, commit, commit},
	} {
		if got, err := g.ExportContained(repo, c.start, c.commit, c.target); err != nil || got {
			t.Errorf("empty export = %v, %v; want false", got, err)
		}
	}
	for _, ids := range [][3]string{{"main", commit, start}, {start, strings.Repeat("a", 40), start}, {start, commit, strings.Repeat("b", 40)}} {
		if _, err := g.ExportContained(repo, ids[0], ids[1], ids[2]); err == nil {
			t.Errorf("accepted invalid/missing commits %v", ids)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "AUTO_MERGE")); !os.IsNotExist(err) {
		t.Errorf("detection left AUTO_MERGE: %v", err)
	}
}

func TestFetchTargetSeparatesURLsAndTargetBranches(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	source := newTestRepo(t)
	first := rev(t, source, "HEAD")
	second := commitOn(t, source, "other", "other target", map[string]string{"f.txt": "other\n"})
	a, b := bareRemote(t), bareRemote(t)
	tg(t, source, "push", a, "main", "other")
	tg(t, source, "push", b, "other:main")
	for _, c := range []struct{ url, base, want string }{{a, "main", first}, {a, "other", second}, {b, "main", second}} {
		if got, err := g.FetchTarget(context.Background(), repo, Remote{Name: "origin", URL: c.url}, c.base); err != nil || got != c.want {
			t.Fatalf("FetchTarget = %s, %v; want %s", got, err, c.want)
		}
	}
	if got := strings.Fields(tg(t, repo, "for-each-ref", "--format=%(refname)", "refs/omo/export-target/")); len(got) != 3 {
		t.Fatalf("different remote URLs/bases share target refs: %v", got)
	}
	// Refreshing a target replaces its own ref and returns its new commit.
	want := commitOn(t, source, "main", "advance", map[string]string{"new.txt": "new\n"})
	tg(t, source, "push", a, "main")
	if got, err := g.FetchTarget(context.Background(), repo, Remote{Name: "renamed", URL: a}, "main"); err != nil || got != want {
		t.Fatalf("refreshed target = %s, %v; want %s", got, err, want)
	}
	if got := strings.Fields(tg(t, repo, "for-each-ref", "--format=%(refname)", "refs/omo/export-target/")); len(got) != 3 {
		t.Fatalf("remote name was used as target identity: %v", got)
	}
}

func TestFetchTargetSerializesRepositoryFetches(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	tg(t, repo, "push", remote, "main")
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	g.pause = func(point string) {
		if point == "fetch-target" {
			entered <- struct{}{}
			if calls.Add(1) == 1 {
				<-release
			}
		}
	}
	errs := make(chan error, 2)
	fetch := func() {
		_, err := g.FetchTarget(context.Background(), repo, Remote{Name: "origin", URL: remote}, "main")
		errs <- err
	}
	go fetch()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("first fetch did not start")
	}
	go fetch()
	select {
	case <-entered:
		t.Error("a second fetch entered while the repository's first fetch was running")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestExportContainedAfterRemoteExportBranchDeletion(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	start := rev(t, repo, "HEAD")
	commit := commitOn(t, repo, "exported", "export", map[string]string{"f.txt": "exported\n"})
	tg(t, repo, "push", remote, "main", "exported")
	if err := g.KeepCommits(repo, 7, []string{start, commit}); err != nil {
		t.Fatal(err)
	}
	integration := filepath.Join(t.TempDir(), "integration")
	tg(t, filepath.Dir(integration), "clone", "-q", "-b", "main", remote, integration)
	tg(t, integration, "merge", "--squash", "refs/remotes/origin/exported")
	tg(t, integration, "commit", "-m", "squashed export")
	tg(t, integration, "push", "origin", "main", ":exported")
	tg(t, repo, "branch", "-D", "exported")
	tg(t, repo, "reflog", "expire", "--expire=now", "--all")
	tg(t, repo, "gc", "--prune=now")
	target, err := g.FetchTarget(context.Background(), repo, Remote{Name: "origin", URL: remote}, "main")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := g.ExportContained(repo, start, commit, target); err != nil || !got {
		t.Fatalf("after remote branch deletion = %v, %v; want true", got, err)
	}
}

func TestExportContainedRefusesAgentFolder(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	id := rev(t, repo, "HEAD")
	g.SetAgentFolders(repo)
	if _, err := g.ExportContained(repo, id, id, id); !errors.Is(err, ErrAgentFolder) {
		t.Fatalf("ExportContained error = %v, want ErrAgentFolder", err)
	}
}
