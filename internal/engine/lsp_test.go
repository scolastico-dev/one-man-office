package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/scolastico-dev/one-man-office/internal/lsp"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

func TestLSPAttachesAllPMWorktreesAndDetachesOnEnd(t *testing.T) {
	h := newHarness(t, withProjects(2))
	story := h.story("Monitor both", h.ids...)
	h.approve(story)
	h.step()
	pm := h.live(api.RolePM, story)
	want := []string{WorktreeDir(h.layout.Worktrees, h.ids[0], story), WorktreeDir(h.layout.Worktrees, h.ids[1], story)}
	if got := h.e.LSP().Worktrees(pm.ID); len(got) != 2 || !samePath(got[0], want[0]) || !samePath(got[1], want[1]) {
		t.Fatalf("monitored %v, want %v", got, want)
	}
	h.e.end(context.Background(), pm.ID, "ended")
	if got := h.e.LSP().Worktrees(pm.ID); len(got) != 0 {
		t.Fatalf("monitor outlived agent: %v", got)
	}
}
func TestLSPDetachesAfterFailedAgentStart(t *testing.T) {
	h := newHarness(t)
	h.runner.setErr(errors.New("cannot start"))
	task := h.standalone("Failed agent")
	h.approve(task)
	h.step()
	sessions := h.sessions(task)
	if len(sessions) != 1 {
		t.Fatal("missing failed session")
	}
	if got := h.e.LSP().Worktrees(sessions[0].ID); len(got) != 0 {
		t.Fatalf("failed start retained monitor: %v", got)
	}
}

// This subprocess is a local stdio server, launched by the production LSP
// runtime in the real worktree prepared by the engine harness.
func TestEngineLSPHelper(t *testing.T) {
	if os.Getenv("ENGINE_LSP_HELPER") != "1" {
		return
	}
	r := bufio.NewReader(os.Stdin)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			os.Exit(0)
		}
		var n int
		if _, err = fmt.Sscanf(line, "Content-Length: %d", &n); err != nil {
			os.Exit(2)
		}
		if _, err = r.ReadString('\n'); err != nil {
			os.Exit(2)
		}
		b := make([]byte, n)
		if _, err = io.ReadFull(r, b); err != nil {
			os.Exit(2)
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(b, &m)
		send := func(v any) {
			b, _ := json.Marshal(v)
			_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(b), b)
		}
		switch m.Method {
		case "initialize":
			var p struct {
				RootPath string `json:"rootPath"`
			}
			_ = json.Unmarshal(m.Params, &p)
			cwd, _ := os.Getwd()
			if cwd != p.RootPath {
				os.Exit(3)
			}
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"capabilities": map[string]any{"textDocumentSync": 1}}})
		case "textDocument/didOpen", "textDocument/didChange":
			var p struct {
				TextDocument struct {
					URI  string `json:"uri"`
					Text string `json:"text"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			}
			_ = json.Unmarshal(m.Params, &p)
			text := p.TextDocument.Text
			if len(p.ContentChanges) > 0 {
				text = p.ContentChanges[0].Text
			}
			send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": p.TextDocument.URI, "diagnostics": []any{map[string]any{"range": map[string]any{"start": map[string]int{"line": 0, "character": 0}, "end": map[string]int{"line": 0, "character": 1}}, "message": text, "severity": 1}}}})
		}
	}
}

func TestLSPRunsAndReportsInRealManagedWorktree(t *testing.T) {
	h := newHarness(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	g := h.st.General()
	g.LSP = api.LSPSettings{Servers: []api.LSPServer{{Name: "fake", Enabled: true, Command: exe, Args: []string{"-test.run=^TestEngineLSPHelper$"}, Env: map[string]string{"ENGINE_LSP_HELPER": "1"}, Files: []api.LSPFileSelector{{Pattern: "*.txt", LanguageID: "text"}}}}}
	if err := h.st.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	h.e.lsp.Close()
	h.e.lsp = lsp.New(lsp.Options{Worktrees: h.layout.Worktrees, Settings: func() api.LSPSettings { return h.st.General().LSP }, PollInterval: time.Hour})
	var reportsMu sync.Mutex
	reports := []lsp.Report{}
	unsubscribe := h.e.LSP().Subscribe(func(r lsp.Report) { reportsMu.Lock(); reports = append(reports, r); reportsMu.Unlock() })
	defer unsubscribe()
	task := h.standalone("Diagnose edits")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	h.waitFor("initial LSP baseline", func() bool { return len(h.e.LSP().Snapshot(dev.ID)) == 1 })
	staged := filepath.Join(dev.Workdir, "a.txt.pending")
	if err := os.WriteFile(staged, []byte("new diagnostic"), 0600); err != nil {
		t.Fatal(err)
	}
	// A scan before publication can only see the complete old file. The
	// following rename publishes the complete new document in one operation.
	if err := h.e.LSP().Scan(h.ctx, dev.Workdir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, filepath.Join(dev.Workdir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := h.e.LSP().Scan(h.ctx, dev.Workdir); err != nil {
		t.Fatal(err)
	}
	h.waitFor("new diagnostic report", func() bool { reportsMu.Lock(); defer reportsMu.Unlock(); return len(reports) > 0 })
	reportsMu.Lock()
	observed := append([]lsp.Report{}, reports...)
	reportsMu.Unlock()
	if len(observed) != 1 {
		t.Fatalf("intermediate write produced additional reports: %+v", observed)
	}
	report := observed[0]
	if len(report.Issues) != 1 || report.Issues[0].Message != "new diagnostic" || !samePath(report.Worktree, dev.Workdir) || !slices.Equal(report.Sessions, []string{dev.ID}) {
		t.Fatalf("report: %+v", report)
	}
	if !h.e.LSP().Ignore(dev.ID, report.Issues[0].ID) {
		t.Fatal("cannot suppress worktree issue")
	}
	h.e.end(context.Background(), dev.ID, "finished")
}

func TestLSPDoesNotAttachToEndedSession(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("End during start")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	_, snapshot, _, err := h.e.read(h.ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	var projects []board.EngineProject
	for _, ticket := range snapshot.Tickets {
		if ticket.ID == task {
			projects = ticket.Projects
		}
	}
	h.e.end(h.ctx, dev.ID, "ended")
	// End may win while spawn is still validating Git records; attaching after
	// that validation must check the engine entry again.
	h.e.attachLSP(h.ctx, spawnRequest{ticket: task, role: api.RoleDeveloper, purpose: api.PurposeWork, workdir: dev.Workdir, worktrees: projects}, dev.ID)
	if got := h.e.LSP().Worktrees(dev.ID); len(got) != 0 {
		t.Fatalf("ended session reattached: %v", got)
	}
}
