//go:build !windows

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

func TestExportMergeKeepsSSHUserAfterRemoteConfigChanges(t *testing.T) {
	for _, change := range []string{"removed", "replaced"} {
		t.Run(change, func(t *testing.T) {
			h := gitHarness(t)
			bare := h.origin(h.project)
			bin := t.TempDir()
			program, marker := filepath.Join(bin, "ssh"), filepath.Join(bin, "ssh-user")
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
			script := "#!/bin/sh\nlogin=\nhost=\ncommand=\nwhile [ \"$#\" -gt 0 ]; do\nif [ \"$1\" = -l ]; then shift; login=$1; fi\ncase \"$1\" in\nforge.example) host=forge.example;;\ngit@forge.example) login=git; host=forge.example;;\nesac\ncommand=$1\nshift\ndone\nif [ \"$login\" != git ] || [ \"$host\" != forge.example ]; then echo 'missing pinned SSH identity' >&2; exit 1; fi\nprintf '%s' \"$login\" > " + quote(marker) + "\nunset GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES\ncase \"$command\" in\ngit-upload-pack*) exec git-upload-pack " + quote(bare) + ";;\ngit-receive-pack*) exec git-receive-pack " + quote(bare) + ";;\n*) echo 'unexpected SSH command' >&2; exit 1;;\nesac\n"
			if err := os.WriteFile(program, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_SSH_COMMAND", quote(program))
			t.Setenv("GIT_SSH_VARIANT", "ssh")
			h.git.SetEnvironment(session.ProcessEnvironment(nil))
			gitRun(t, h.repo, "remote", "set-url", "origin", "ssh://git@forge.example/org/repo.git")
			n, job, _, branch := h.exportedTask("Keep SSH identity")
			rec, found, err := h.e.pins.pushed(n, h.project)
			if err != nil || !found {
				t.Fatalf("push record = %+v, %v, %v", rec, found, err)
			}
			if rec.SSHUser != "git" || rec.Address != "ssh://forge.example/org/repo.git" {
				t.Fatalf("export did not retain SSH transport identity: %+v", rec)
			}
			if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
				t.Fatal(err)
			}
			h.steps(2)
			gitRun(t, h.repo, "merge", "-q", "--no-ff", "-m", "land export", branch)
			gitRun(t, h.repo, "push", "-q", bare, "refs/heads/main:refs/heads/main")
			if change == "removed" {
				gitRun(t, h.repo, "remote", "remove", "origin")
			} else {
				gitRun(t, h.repo, "remote", "set-url", "origin", "ssh://different@other.example/unrelated.git")
			}
			if err := os.WriteFile(marker, nil, 0600); err != nil {
				t.Fatal(err)
			}
			h.e.checkExportsMerged(h.ctx)
			if tk := h.ticket(n); tk.Column != api.ColumnDone || !tk.ExportMerged {
				t.Fatalf("merge check lost recorded SSH identity: %+v; logs: %s", tk.TicketSummary, h.logs.String())
			}
			login, err := os.ReadFile(marker)
			if err != nil || string(login) != "git" {
				t.Fatalf("SSH login = %q, %v; want git", login, err)
			}
		})
	}
}
