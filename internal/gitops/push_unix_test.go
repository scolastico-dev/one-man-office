//go:build !windows

// The planted programs are shell commands, which Windows cannot run.
package gitops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plantHook writes an executable hook that touches marker.
func plantHook(t *testing.T, dir, name, marker string) {
	t.Helper()
	p := filepath.Join(dir, name)
	write(t, p, "#!/bin/sh\ntouch '"+marker+"'\nexit 0\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPushCommitRunsNoHooks(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	commit := commitOn(t, repo, "omo/1-x", "x", map[string]string{"f.txt": "x\n"})
	marker := filepath.Join(t.TempDir(), "ran")
	// In the project's hooks folder, in a folder its config names, and in
	// the receiving repository on this machine.
	planted := t.TempDir()
	for _, h := range []string{"pre-push", "reference-transaction", "post-update", "push-to-checkout"} {
		plantHook(t, filepath.Join(repo, ".git", "hooks"), h, marker)
		plantHook(t, planted, h, marker)
	}
	for _, h := range []string{"pre-receive", "update", "post-receive", "post-update", "reference-transaction", "proc-receive"} {
		plantHook(t, filepath.Join(remote, "hooks"), h, marker)
	}
	tg(t, repo, "config", "core.hooksPath", planted)
	tg(t, repo, "config", "core.fsmonitor", "touch '"+marker+"'; false #")
	if err := g.PushCommit(context.Background(), repo, Remote{Name: "origin", URL: remote}, commit, "omo/1-x"); err != nil {
		t.Fatal(err)
	}
	noMarker(t, marker)
	if got := remoteRef(t, remote, "omo/1-x"); got != commit {
		t.Fatalf("remote has %q, want %s", got, commit)
	}
}

// fakeSSH puts an ssh on PATH that records it ran and fails, so a push to
// an ssh address never leaves the machine.
func fakeSSH(t *testing.T) (ran string) {
	t.Helper()
	bin := t.TempDir()
	ran = filepath.Join(t.TempDir(), "ssh-ran")
	write(t, filepath.Join(bin, "ssh"), "#!/bin/sh\ntouch '"+ran+"'\necho 'fake ssh: no connection' >&2\nexit 255\n")
	if err := os.Chmod(filepath.Join(bin, "ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return ran
}

func TestPushCommitIgnoresRepositorySSHCommand(t *testing.T) {
	ran := fakeSSH(t)
	g := testGit(t)
	repo := newTestRepo(t)
	commit := rev(t, repo, "HEAD")
	marker := filepath.Join(t.TempDir(), "ran")
	tg(t, repo, "config", "core.sshCommand", "touch '"+marker+"'; false")
	tg(t, repo, "config", "ssh.variant", "simple")
	err := g.PushCommit(context.Background(), repo, Remote{Name: "origin", URL: "ssh://git@127.0.0.1/o/r.git"}, commit, "omo/1-x")
	if err == nil {
		t.Fatal("the push succeeded")
	}
	noMarker(t, marker)
	if _, err := os.Stat(ran); err != nil {
		t.Errorf("git did not use the user's ssh: %v", err)
	}
	if !strings.Contains(err.Error(), "fake ssh: no connection") {
		t.Errorf("the error does not carry git's message: %v", err)
	}
}

func TestPushCommitIgnoresRepositoryCredentialHelper(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	userRan := filepath.Join(t.TempDir(), "user-helper")
	// The user's own helper is consulted; it knows nothing here.
	g := gitWithGlobal(t, "[http]\n\tsslVerify = false\n[credential]\n\thelper = \"!touch '"+userRan+"'; true\"\n")
	repo := newTestRepo(t)
	commit := rev(t, repo, "HEAD")
	marker := filepath.Join(t.TempDir(), "ran")
	tg(t, repo, "config", "--add", "credential.helper", "!touch '"+marker+"'; echo password=planted")
	tg(t, repo, "config", "--add", "credential."+srv.URL+".helper", "!touch '"+marker+"'; true")
	tg(t, repo, "config", "credential.useHttpPath", "true")
	tg(t, repo, "config", "core.askPass", "touch '"+marker+"'")
	err := g.PushCommit(context.Background(), repo, Remote{Name: "origin", URL: srv.URL + "/o/r.git"}, commit, "omo/1-x")
	if err == nil {
		t.Fatal("the push succeeded")
	}
	noMarker(t, marker)
	if _, err := os.Stat(userRan); err != nil {
		t.Errorf("the user's credential helper was not asked: %v", err)
	}
}
