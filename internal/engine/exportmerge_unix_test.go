//go:build !windows

package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

func TestShutdownCancelsExportFetch(t *testing.T) {
	h := gitHarness(t)
	n, job, _, _ := h.exportedTask("Cancel check")
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "fetching")
	script := "#!/bin/sh\nfor arg do\nif [ \"$arg\" = fetch ]; then\necho started > " + strconv.Quote(marker) + "\nexec sleep 10\nfi\ndone\nexec " + strconv.Quote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h.git.SetEnvironment(session.ProcessEnvironment([]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}))
	h.e.startExportCheck(h.ctx, h.clock.Now(), true)
	h.waitFor("fetch process", func() bool { _, err := os.Stat(marker); return err == nil })
	ended := make(chan struct{})
	go func() { h.e.halt(h.ctx, "test shutdown"); close(ended) }()
	select {
	case <-ended:
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown did not cancel background fetch")
	}
	if h.ticket(n).Column == api.ColumnDone {
		t.Fatal("cancelled fetch completed export")
	}
}
