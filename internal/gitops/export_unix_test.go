//go:build !windows

package gitops

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchTargetIgnoresRepositoryHooksAndURLRewrites(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	source := newTestRepo(t)
	remote := bareRemote(t)
	tg(t, source, "push", remote, "main")
	marker := filepath.Join(t.TempDir(), "ran")
	planted := t.TempDir()
	plantHook(t, filepath.Join(repo, ".git", "hooks"), "reference-transaction", marker)
	plantHook(t, planted, "reference-transaction", marker)
	tg(t, repo, "config", "core.hooksPath", planted)
	tg(t, repo, "config", "url.ext::false.insteadOf", remote)
	tg(t, repo, "config", "fetch.writeCommitGraph", "true")
	tg(t, repo, "config", "fetch.recurseSubmodules", "true")
	if got, err := g.FetchTarget(context.Background(), repo, Remote{Name: "origin", URL: remote}, "main"); err != nil || got != rev(t, source, "HEAD") {
		t.Fatalf("FetchTarget = %q, %v", got, err)
	}
	noMarker(t, marker)
}

func TestFetchTargetIgnoresRepositoryCredentialHelper(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	userRan := filepath.Join(t.TempDir(), "user-helper")
	g := gitWithGlobal(t, "[http]\n\tsslVerify = false\n[credential]\n\thelper = \"!touch '"+userRan+"'; true\"\n")
	repo := newTestRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	tg(t, repo, "config", "credential.helper", "!touch '"+marker+"'; true")
	if _, err := g.FetchTarget(context.Background(), repo, Remote{Name: "origin", URL: srv.URL + "/repo.git"}, "main"); err == nil {
		t.Fatal("unauthenticated fetch succeeded")
	}
	noMarker(t, marker)
	if _, err := os.Stat(userRan); err != nil {
		t.Errorf("user credential helper was not consulted: %v", err)
	}
}

func TestFetchTargetTimeout(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	bin := t.TempDir()
	program := filepath.Join(bin, "ssh")
	write(t, program, "#!/bin/sh\nsleep 30\n")
	if err := os.Chmod(program, 0o755); err != nil {
		t.Fatal(err)
	}
	g.SetEnvironment(append(isolatedEnv(t), "GIT_SSH_COMMAND="+program, "GIT_SSH_VARIANT=ssh"))
	g.op = 100 * time.Millisecond
	g.grace = 20 * time.Millisecond
	if _, err := g.FetchTarget(context.Background(), repo, Remote{Name: "origin", URL: "ssh://git@127.0.0.1/repo.git"}, "main"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("fetch error = %v, want ErrTimeout", err)
	}
}

func TestExportContainedGit238Fallback(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	start := rev(t, repo, "HEAD")
	commit := commitOn(t, repo, "exported", "change", map[string]string{"f.txt": "exported\n"})
	commitOn(t, repo, "main", "base advancement", map[string]string{"other.txt": "advanced\n"})
	tg(t, repo, "merge", "--squash", "exported")
	tg(t, repo, "commit", "-m", "squash export")
	target := rev(t, repo, "HEAD")
	// An actual git still performs the merge; the wrapper only emulates
	// 2.38's refusal of the newer explicit-base option.
	bin := t.TempDir()
	program := filepath.Join(bin, "git")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	write(t, program, "#!/bin/sh\nfor arg in \"$@\"; do\ncase \"$arg\" in --merge-base=*) echo 'error: unknown option merge-base' >&2; exit 129;; esac\ndone\nexec '"+strings.ReplaceAll(realGit, "'", "'\\''")+"' \"$@\"\n")
	if err := os.Chmod(program, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got, err := g.ExportContained(repo, start, commit, target); err != nil || !got {
		t.Fatalf("Git 2.38 content inclusion = %v, %v; want true", got, err)
	}
	if got, err := g.ExportContained(repo, start, commit, start); err != nil || got {
		t.Fatalf("Git 2.38 unchanged target = %v, %v; want false", got, err)
	}
}
