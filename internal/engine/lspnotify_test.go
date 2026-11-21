package engine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/lsp"
)

// fakeListeners stands for the MCP notification streams of agent sessions.
type fakeListeners struct {
	mu        sync.Mutex
	confirmed map[string]bool
	got       map[string][]string
}

func newFakeListeners() *fakeListeners {
	return &fakeListeners{confirmed: map[string]bool{}, got: map[string][]string{}}
}

func (f *fakeListeners) set(session string, on bool) {
	f.mu.Lock()
	f.confirmed[session] = on
	f.mu.Unlock()
}

func (f *fakeListeners) Notify(session, text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.confirmed[session] {
		return false
	}
	f.got[session] = append(f.got[session], text)
	return true
}

func (f *fakeListeners) received(session string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.got[session])
}

// lspNotifyHarness runs the production LSP runtime with the fake language
// server of TestEngineLSPHelper in the real worktrees the harness makes.
func lspNotifyHarness(t *testing.T, forceChat ...string) (*harness, *fakeListeners) {
	t.Helper()
	h := newHarness(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	g := h.st.General()
	g.LSP = api.LSPSettings{
		Servers: []api.LSPServer{{Name: "fake", Enabled: true, Command: exe, Args: []string{"-test.run=^TestEngineLSPHelper$"},
			Env: map[string]string{"ENGINE_LSP_HELPER": "1"}, Files: []api.LSPFileSelector{{Pattern: "*.txt", LanguageID: "text"}}}},
		ForceChat: append([]string{}, forceChat...),
	}
	if err := h.st.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	h.e.lsp.Close()
	h.e.lsp = lsp.New(lsp.Options{Worktrees: h.layout.Worktrees, Settings: func() api.LSPSettings { return h.st.General().LSP }, PollInterval: time.Hour})
	listeners := newFakeListeners()
	h.e.o.Listeners = listeners
	h.e.subscribeLSP()
	h.typeAtOnce()
	return h, listeners
}

// diagnose makes the fake language server report message in dev's worktree
// and waits until the runtime has seen it.
func (h *harness) diagnose(dev board.EngineSession, file, message string) {
	h.t.Helper()
	staged := filepath.Join(dev.Workdir, file+".pending")
	if err := os.WriteFile(staged, []byte(message), 0600); err != nil {
		h.t.Fatal(err)
	}
	if err := os.Rename(staged, filepath.Join(dev.Workdir, file)); err != nil {
		h.t.Fatal(err)
	}
	if err := h.e.LSP().Scan(h.ctx, dev.Workdir); err != nil {
		h.t.Fatal(err)
	}
}

// developer starts a ready developer on a new task, waits for its baseline.
func (h *harness) lspDeveloper(title string) board.EngineSession {
	h.t.Helper()
	_, dev := h.readyDeveloper(title)
	h.waitFor("the baseline of "+title, func() bool { return len(h.e.LSP().Snapshot(dev.ID)) == 1 })
	return dev
}

func (h *harness) chatLines(session string) []string {
	h.t.Helper()
	return h.agent(session).typed()
}

func hasLine(lines []string, part string) bool {
	return slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, part) })
}

func TestLSPConfirmedListenerGetsReportOverMCPOnly(t *testing.T) {
	h, listeners := lspNotifyHarness(t)
	dev := h.lspDeveloper("Listening")
	listeners.set(dev.ID, true)
	h.diagnose(dev, "a.txt", "first problem")
	h.waitFor("the MCP notification", func() bool { return len(listeners.received(dev.ID)) == 1 })
	text := listeners.received(dev.ID)[0]
	for _, want := range []string{"first problem", "a.txt:1:1", "omo ai lsp ignore", "error"} {
		if !strings.Contains(text, want) {
			t.Errorf("notification lacks %q: %s", want, text)
		}
	}
	if lines := h.chatLines(dev.ID); hasLine(lines, "first problem") {
		t.Fatalf("a listening agent was also nudged in chat: %v", lines)
	}
}

func TestLSPWithoutConfirmedListenerFallsBackToChat(t *testing.T) {
	h, listeners := lspNotifyHarness(t)
	dev := h.lspDeveloper("Not listening")
	h.diagnose(dev, "a.txt", "chat problem")
	h.waitFor("the chat line", func() bool { return hasLine(h.chatLines(dev.ID), "chat problem") })
	if got := listeners.received(dev.ID); len(got) != 0 {
		t.Fatalf("an unconfirmed session received over MCP: %v", got)
	}
}

func TestLSPDisconnectedListenerNoLongerSuppressesChat(t *testing.T) {
	h, listeners := lspNotifyHarness(t)
	dev := h.lspDeveloper("Disconnects")
	listeners.set(dev.ID, true)
	h.diagnose(dev, "a.txt", "while listening")
	h.waitFor("the MCP notification", func() bool { return len(listeners.received(dev.ID)) == 1 })
	listeners.set(dev.ID, false)
	h.diagnose(dev, "a.txt", "after disconnect")
	h.waitFor("the chat line", func() bool { return hasLine(h.chatLines(dev.ID), "after disconnect") })
	if got := listeners.received(dev.ID); len(got) != 1 {
		t.Fatalf("the disconnected listener received %v", got)
	}
}

func TestLSPForceChatWinsOverAnActiveListener(t *testing.T) {
	h, listeners := lspNotifyHarness(t, "custom")
	dev := h.lspDeveloper("Forced")
	if cli := h.e.sessionCLI(dev.ID); cli != "custom" {
		t.Fatalf("the harness agent runs %q, the test forces custom", cli)
	}
	listeners.set(dev.ID, true)
	h.diagnose(dev, "a.txt", "forced problem")
	h.waitFor("the chat line", func() bool { return hasLine(h.chatLines(dev.ID), "forced problem") })
	if got := listeners.received(dev.ID); len(got) != 0 {
		t.Fatalf("force-chat still used the listener: %v", got)
	}
}

func TestLSPReportsStayWithTheirOwnSessionAndWorktree(t *testing.T) {
	h, listeners := lspNotifyHarness(t)
	one := h.lspDeveloper("First worktree")
	two := h.lspDeveloper("Second worktree")
	listeners.set(one.ID, true)
	listeners.set(two.ID, true)
	h.diagnose(one, "a.txt", "only for one")
	h.waitFor("one's notification", func() bool { return len(listeners.received(one.ID)) == 1 })
	h.diagnose(two, "a.txt", "only for two")
	h.waitFor("two's notification", func() bool { return len(listeners.received(two.ID)) == 1 })
	if got := listeners.received(one.ID); len(got) != 1 || !strings.Contains(got[0], "only for one") || strings.Contains(got[0], "only for two") {
		t.Fatalf("session one received %v", got)
	}
	if got := listeners.received(two.ID); len(got) != 1 || !strings.Contains(got[0], "only for two") {
		t.Fatalf("session two received %v", got)
	}
	for _, id := range []string{one.ID, two.ID} {
		if lines := h.chatLines(id); hasLine(lines, "only for") {
			t.Fatalf("a listening session was nudged in chat: %v", lines)
		}
	}
}

func TestLSPIgnoredIssueIsNotReportedAgainButANewOneIs(t *testing.T) {
	h, listeners := lspNotifyHarness(t)
	dev := h.lspDeveloper("Ignores")
	listeners.set(dev.ID, true)
	h.diagnose(dev, "a.txt", "bogus finding")
	h.waitFor("the first notification", func() bool { return len(listeners.received(dev.ID)) == 1 })
	issues := h.e.LSP().Snapshot(dev.ID)
	if len(issues) != 1 {
		t.Fatalf("issues %+v", issues)
	}
	res, err := h.e.IgnoreLSPIssue(h.actor(dev.ID), issues[0].ID)
	if err != nil || !res.Ignored || res.File != "a.txt" {
		t.Fatalf("ignore: %+v %v", res, err)
	}
	if _, err := h.e.IgnoreLSPIssue(h.actor(dev.ID), "no-such-issue"); err == nil {
		t.Fatal("an unknown issue was confirmed as ignored")
	}
	// The issue goes away, then comes back together with an unrelated one:
	// only the unrelated one is reported.
	if err := os.Remove(filepath.Join(dev.Workdir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := h.e.LSP().Scan(h.ctx, dev.Workdir); err != nil {
		t.Fatal(err)
	}
	h.diagnose(dev, "a.txt", "bogus finding")
	h.diagnose(dev, "b.txt", "a real problem")
	h.waitFor("the new notification", func() bool { return len(listeners.received(dev.ID)) == 2 })
	if got := listeners.received(dev.ID)[1]; !strings.Contains(got, "a real problem") || strings.Contains(got, "bogus finding") {
		t.Fatalf("the second notification: %s", got)
	}
}

// A report already queued for delivery loses the issues ignored since; a
// report with nothing left is not delivered at all.
func TestLSPQueuedReportDropsIgnoredIssues(t *testing.T) {
	h, listeners := lspNotifyHarness(t)
	dev := h.lspDeveloper("Queued")
	issues := h.e.LSP().Snapshot(dev.ID)
	if len(issues) != 1 {
		t.Fatalf("issues %+v", issues)
	}
	other := issues[0]
	other.ID, other.Message = "other-id", "still wanted"
	listeners.set(dev.ID, true)
	if _, err := h.e.IgnoreLSPIssue(h.actor(dev.ID), issues[0].ID); err != nil {
		t.Fatal(err)
	}
	h.e.deliverLSP(lsp.Report{Worktree: dev.Workdir, Sessions: []string{dev.ID}, Issues: issues})
	if got := listeners.received(dev.ID); len(got) != 0 {
		t.Fatalf("a report of only ignored issues was delivered: %v", got)
	}
	h.e.deliverLSP(lsp.Report{Worktree: dev.Workdir, Sessions: []string{dev.ID}, Issues: []lsp.Issue{issues[0], other}})
	got := listeners.received(dev.ID)
	if len(got) != 1 || !strings.Contains(got[0], "still wanted") || strings.Contains(got[0], issues[0].ID) || !strings.Contains(got[0], "1 new issue ") {
		t.Fatalf("delivered %v", got)
	}
}

// A very long message is cut so the id and the ignore advice survive the
// notification line's length limit, in chat too.
func TestLSPLongMessageKeepsTheIDInChat(t *testing.T) {
	h, _ := lspNotifyHarness(t)
	dev := h.lspDeveloper("Long")
	long := strings.Repeat("type mismatch ", 200)
	h.diagnose(dev, "a.txt", long)
	h.waitFor("the chat line", func() bool { return hasLine(h.chatLines(dev.ID), "type mismatch") })
	var line string
	for _, l := range h.chatLines(dev.ID) {
		if strings.Contains(l, "type mismatch") {
			line = l
		}
	}
	snap := h.e.LSP().Snapshot(dev.ID)
	if len(snap) != 1 || !strings.Contains(line, "(id "+snap[0].ID+")") || !strings.Contains(line, "omo ai lsp ignore") {
		t.Fatalf("the id or the advice was cut: %q", line)
	}
}
