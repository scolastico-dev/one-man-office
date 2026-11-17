package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

// shippedPluginRepo makes a local bare repository holding the files of the
// repository's plugins/<name> and returns its folder.
func shippedPluginRepo(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("..", "..", "plugins", name)
	work := t.TempDir()
	bare := filepath.Join(t.TempDir(), "plugin.git")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, e.Name()), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(work, "init", "-q", "-b", "main")
	run(work, "add", ".")
	run(work, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "plugin")
	run(work, "clone", "-q", "--bare", work, bare)
	return bare
}

// TestPauseNoticeTellsRunningAgents installs plugins/pause-notice and
// switches the pause on and off: every agent the engine runs gets the halt
// text through its terminal's notification line (the CEO its own), and the
// continue text after the pause; no comment is written, and an agent that
// is not running gets nothing.
func TestPauseNoticeTellsRunningAgents(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	b := newBrowser(t, s)
	repo := shippedPluginRepo(t, "pause-notice")
	resp := asUser(b, s, "POST", "/api/plugins", community(api.PluginSettings{Name: "pause-notice", Source: repo, Enabled: true}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("install: status %d: %s", resp.StatusCode, respText(t, resp))
	}
	manifest, err := plugins.ReadManifest(filepath.Join(s.opts.Layout.Plugins, "pause-notice"))
	if err != nil {
		t.Fatal(err)
	}
	halt := manifest.DefaultConfig["halt_message"].(string)
	ceoHalt := manifest.DefaultConfig["ceo_halt_message"].(string)
	resume := manifest.DefaultConfig["resume_message"].(string)

	n, dev := startDeveloper(t, s, p)
	wantStatus(t, socketDo(t, s, "POST", "/api/ai/ready", empty, dev.ID).Code, http.StatusOK)
	resp = asUser(b, s, "POST", "/api/ceo", empty)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	ceo := body[api.AgentSession](t, resp)
	wantStatus(t, socketDo(t, s, "POST", "/api/ai/ready", empty, ceo.ID).Code, http.StatusOK)
	devAgent, ceoAgent := r.agent(t, dev.ID), r.agent(t, ceo.ID)
	lines := func(a *testAgent) []string {
		a.mu.Lock()
		defer a.mu.Unlock()
		return slices.Clone(a.lines)
	}
	before := len(lines(devAgent))

	wantStatus(t, asUser(b, s, "PUT", "/api/server/paused", map[string]bool{"paused": true}).StatusCode, http.StatusOK)
	waitFor(t, "the halt texts", func() bool { return len(lines(devAgent)) > before && len(lines(ceoAgent)) > 0 })
	if got := lines(devAgent)[before:]; !slices.Equal(got, []string{halt}) {
		t.Fatalf("developer got %q, want the halt text", got)
	}
	if got := lines(ceoAgent); !slices.Equal(got, []string{ceoHalt}) {
		t.Fatalf("CEO got %q, want its halt text", got)
	}

	wantStatus(t, asUser(b, s, "PUT", "/api/server/paused", map[string]bool{"paused": false}).StatusCode, http.StatusOK)
	waitFor(t, "the resume texts", func() bool { return len(lines(devAgent)) > before+1 && len(lines(ceoAgent)) > 1 })
	if got := lines(devAgent)[before:]; !slices.Equal(got, []string{halt, resume}) {
		t.Fatalf("developer got %q", got)
	}
	if got := lines(ceoAgent); !slices.Equal(got, []string{ceoHalt, resume}) {
		t.Fatalf("CEO got %q", got)
	}

	comments, err := s.board.Comments(context.Background(), board.User, int64(n))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range comments {
		if strings.Contains(c.Body, "paused") || strings.Contains(c.Body, "pause is over") {
			t.Fatalf("a comment was written: %q", c.Body)
		}
	}
	raw, _ := json.Marshal(s.plugins.m.LogLines("pause-notice", 50))
	if !strings.Contains(string(raw), "told 2 agents to halt") || !strings.Contains(string(raw), "told 2 agents to continue") {
		t.Fatalf("log %s", raw)
	}
}
