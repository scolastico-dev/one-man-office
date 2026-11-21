package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"net/url"
)

// The fake is a real stdio child. It validates root/cwd and protocol content,
// and returns diagnostic text derived from the synchronized document.
func TestLSPHelper(t *testing.T) {
	if os.Getenv("LSP_TEST_HELPER") != "1" {
		return
	}
	mode := os.Getenv("LSP_TEST_MODE")
	r := bufio.NewReader(os.Stdin)
	docs := map[string]string{}
	versions := map[string]int{}
	refreshed := false
	pullErrorSent := false
	pullResults := map[string]string{}
	respond := func(id json.RawMessage, result any) {
		b, _ := json.Marshal(result)
		if err := writeMessage(os.Stdout, message{JSONRPC: "2.0", ID: id, Result: b}); err != nil {
			os.Exit(3)
		}
	}
	publish := func(uri, text string) {
		ds := []diagnostic{}
		if strings.Contains(text, "bad") {
			ds = append(ds, diagnostic{Message: text, Severity: 1, Range: Range{Start: Position{Line: 1, Character: 2}}})
		}
		params := map[string]any{"uri": uri, "version": versions[uri], "diagnostics": ds}
		if mode == "disk" {
			delete(params, "version")
		}
		b, _ := json.Marshal(params)
		_ = writeMessage(os.Stdout, message{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: b})
	}
	for {
		m, err := readMessage(r)
		if err != nil {
			os.Exit(0)
		}
		var p struct {
			PreviousResultID string `json:"previousResultId"`
			Identifier       string `json:"identifier"`
			RootURI          string `json:"rootUri"`
			WorkspaceFolders []struct {
				URI string `json:"uri"`
			} `json:"workspaceFolders"`
			InitializationOptions map[string]any `json:"initializationOptions"`
			TextDocument          struct {
				URI        string `json:"uri"`
				Text       string `json:"text"`
				LanguageID string `json:"languageId"`
				Version    int    `json:"version"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text  string `json:"text"`
				Range *Range `json:"range"`
			} `json:"contentChanges"`
			Changes []struct {
				URI  string `json:"uri"`
				Type int    `json:"type"`
			} `json:"changes"`
		}
		_ = json.Unmarshal(m.Params, &p)
		switch m.Method {
		case "initialize":
			cwd, _ := os.Getwd()
			if p.RootURI != fileURI(cwd) || len(p.WorkspaceFolders) != 1 || p.WorkspaceFolders[0].URI != fileURI(cwd) || p.InitializationOptions["test"] != "yes" {
				os.Exit(4)
			}
			caps := map[string]any{"textDocumentSync": map[string]any{"openClose": true, "change": 2, "save": map[string]any{"includeText": true}}}
			if mode == "full" {
				caps["textDocumentSync"] = 1
			}
			if mode == "disk" {
				caps["textDocumentSync"] = map[string]any{"openClose": false, "change": 0}
			}
			if mode == "pull" || mode == "workspace" || mode == "related" || mode == "pull-watch" || mode == "pull-error" || mode == "workspace-error" {
				caps["diagnosticProvider"] = map[string]any{"identifier": "test", "interFileDependencies": true, "workspaceDiagnostics": mode == "workspace" || mode == "workspace-error"}
			}
			respond(m.ID, map[string]any{"capabilities": caps})
		case "initialized":
			if mode == "watchall" {
				b, _ := json.Marshal(map[string]any{"registrations": []any{map[string]any{"id": "all", "method": "workspace/didChangeWatchedFiles", "registerOptions": map[string]any{"watchers": []any{map[string]any{"globPattern": "**/*"}}}}}})
				_ = writeMessage(os.Stdout, message{JSONRPC: "2.0", ID: json.RawMessage(`"watch"`), Method: "client/registerCapability", Params: b})
			}
			if mode == "disk" || mode == "pull-watch" {
				b, _ := json.Marshal(map[string]any{"registrations": []any{map[string]any{"id": "watch", "method": "workspace/didChangeWatchedFiles", "registerOptions": map[string]any{"watchers": []any{map[string]any{"globPattern": "**/*.test"}, map[string]any{"globPattern": "**/*.cfg"}}}}}})
				_ = writeMessage(os.Stdout, message{JSONRPC: "2.0", ID: json.RawMessage(`"watch"`), Method: "client/registerCapability", Params: b})
			}
		case "workspace/didChangeWatchedFiles":
			if mode == "disk" {
				for _, c := range p.Changes {
					u, _ := url.Parse(c.URI)
					b, _ := os.ReadFile(filepath.FromSlash(u.Path))
					docs[c.URI] = string(b)
					publish(c.URI, string(b))
				}
			}
		case "textDocument/didOpen":
			if p.TextDocument.LanguageID != "test" {
				os.Exit(5)
			}
			docs[p.TextDocument.URI] = p.TextDocument.Text
			versions[p.TextDocument.URI] = p.TextDocument.Version
			if mode == "push" || mode == "full" || mode == "watchall" {
				publish(p.TextDocument.URI, p.TextDocument.Text)
			}
			if mode == "stderr" {
				info, err := os.Stderr.Stat()
				if err != nil {
					os.Exit(9)
				}
				if info.Mode()&os.ModeNamedPipe != 0 {
					publish(p.TextDocument.URI, "bad stderr pipe")
				} else {
					publish(p.TextDocument.URI, "bad stderr file")
				}
			}
		case "textDocument/didChange":
			if len(p.ContentChanges) != 1 || (mode != "full" && p.ContentChanges[0].Range == nil) || (mode == "full" && p.ContentChanges[0].Range != nil) {
				os.Exit(6)
			}
			if mode != "full" {
				line, column := 0, 0
				for _, ch := range docs[p.TextDocument.URI] {
					if ch == '\n' {
						line++
						column = 0
					} else if ch > 0xffff {
						column += 2
					} else {
						column++
					}
				}
				got := p.ContentChanges[0].Range
				if got.Start.Line != 0 || got.Start.Character != 0 || got.End.Line != line || got.End.Character != column {
					os.Exit(8)
				}
			}
			docs[p.TextDocument.URI] = p.ContentChanges[0].Text
			versions[p.TextDocument.URI] = p.TextDocument.Version
			if mode == "push" || mode == "full" || mode == "delayed" || mode == "watchall" {
				publish(p.TextDocument.URI, p.ContentChanges[0].Text)
			}
		case "textDocument/didClose":
			delete(docs, p.TextDocument.URI)
		case "textDocument/diagnostic":
			if mode == "pull-error" && !pullErrorSent {
				pullErrorSent = true
				_ = writeMessage(os.Stdout, message{JSONRPC: "2.0", ID: m.ID, Error: &rpcError{-32801, "Content modified"}})
				continue
			}
			if p.Identifier != "test" {
				os.Exit(7)
			}
			text := docs[p.TextDocument.URI]
			if mode == "pull-watch" {
				cwd, _ := os.Getwd()
				data, _ := os.ReadFile(filepath.Join(cwd, "options.cfg"))
				if strings.Contains(string(data), "bad") {
					text = string(data)
				}
			}
			if refreshed {
				text = "bad refresh"
			}
			if old, ok := pullResults[p.TextDocument.URI]; ok && old == text && p.PreviousResultID == text {
				respond(m.ID, map[string]any{"kind": "unchanged", "resultId": text})
				continue
			}
			ds := []diagnostic{}
			if strings.Contains(text, "bad") {
				ds = append(ds, diagnostic{Message: text, Severity: 1})
			}
			result := map[string]any{"kind": "full", "resultId": text, "items": ds}
			if mode == "related" {
				cwd, _ := os.Getwd()
				result["relatedDocuments"] = map[string]any{fileURI(filepath.Join(cwd, "related.cfg")): map[string]any{"kind": "full", "items": []diagnostic{{Message: "bad related", Severity: 1}}}}
			}
			pullResults[p.TextDocument.URI] = text
			respond(m.ID, result)
		case "workspace/diagnostic":
			if mode == "workspace-error" && !pullErrorSent {
				pullErrorSent = true
				_ = writeMessage(os.Stdout, message{JSONRPC: "2.0", ID: m.ID, Error: &rpcError{-32802, "Server cancelled"}})
				continue
			}
			// Servers can leave open documents to textDocument/diagnostic.
			respond(m.ID, map[string]any{"items": []any{}})
		case "test/baselineUpdate":
			for uri := range docs {
				publish(uri, "bad extended baseline")
			}
			respond(m.ID, nil)
		case "test/refresh":
			refreshed = true
			_ = writeMessage(os.Stdout, message{JSONRPC: "2.0", ID: json.RawMessage(`"refresh"`), Method: "workspace/diagnostic/refresh", Params: json.RawMessage(`{}`)})
			respond(m.ID, nil)
		case "test/exit":
			os.Exit(0)
		case "test/barrier":
			respond(m.ID, map[string]any{"documents": docs})
		case "shutdown":
			respond(m.ID, nil)
		case "exit":
			os.Exit(0)
		default:
			if len(m.ID) > 0 {
				respond(m.ID, nil)
			}
		}
	}
}
func fixtureManager(t *testing.T, mode string) (*Manager, string) {
	t.Helper()
	childenvtest.ClearOmo(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "project", "agent")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: managed"), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.LSPSettings{Debounce: api.Duration(time.Minute), Servers: []api.LSPServer{{Name: "test", Enabled: true, Command: exe, Args: []string{"-test.run=^TestLSPHelper$"}, Env: map[string]string{"LSP_TEST_HELPER": "1", "LSP_TEST_MODE": mode}, Files: []api.LSPFileSelector{{Pattern: "*.test", LanguageID: "test"}}, InitializationOptions: map[string]any{"test": "yes"}}}}
	m := New(Options{Worktrees: base, Settings: func() api.LSPSettings { return cfg }, PollInterval: time.Hour})
	t.Cleanup(m.Close)
	return m, root
}
func putFile(t *testing.T, root, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func scan(t *testing.T, m *Manager, root string) {
	t.Helper()
	if err := m.Scan(t.Context(), root); err != nil {
		t.Fatal(err)
	}
}
func barrier(t *testing.T, m *Manager, root string) map[string]string {
	t.Helper()
	m.mu.Lock()
	r := m.roots[root]
	m.mu.Unlock()
	var result struct {
		Documents map[string]string `json:"documents"`
	}
	if err := r.servers["test"].peer.call(t.Context(), "test/barrier", nil, &result); err != nil {
		t.Fatal(err)
	}
	return result.Documents
}
func TestRuntimeSyncIsolationAndShutdown(t *testing.T) {
	for _, mode := range []string{"push", "full", "pull", "workspace"} {
		t.Run(mode, func(t *testing.T) {
			m, root := fixtureManager(t, mode)
			putFile(t, root, "main.test", "bad baseline")
			if err := m.Attach("first", []string{root}); err != nil {
				t.Fatal(err)
			}
			scan(t, m, root)
			barrier(t, m, root)
			if got := m.Snapshot("first"); len(got) != 1 || got[0].Message != "bad baseline" || got[0].File != "main.test" {
				t.Fatalf("baseline: %+v", got)
			}
			putFile(t, root, "main.test", "😀 bad changed\n")
			scan(t, m, root)
			docs := barrier(t, m, root)
			if docs[fileURI(filepath.Join(root, "main.test"))] != "😀 bad changed\n" {
				t.Fatalf("document not synchronized: %+v", docs)
			}
			putFile(t, root, "main.test", "bad final")
			scan(t, m, root)
			barrier(t, m, root)
			other := filepath.Join(filepath.Dir(root), "other")
			if err := os.Mkdir(other, 0700); err != nil {
				t.Fatal(err)
			}
			putFile(t, other, ".git", "gitdir: managed")
			putFile(t, other, "main.test", "bad isolated")
			if err := m.Attach("second", []string{other}); err != nil {
				t.Fatal(err)
			}
			scan(t, m, other)
			barrier(t, m, other)
			if got := m.Snapshot("second"); len(got) != 1 || got[0].Message != "bad isolated" || got[0].ID == m.Snapshot("first")[0].ID {
				t.Fatalf("isolation: %+v", got)
			}
			putFile(t, root, "created.test", "bad created")
			scan(t, m, root)
			barrier(t, m, root)
			if len(m.Snapshot("first")) != 2 {
				t.Fatal("created file not diagnosed")
			}
			if err := os.Remove(filepath.Join(root, "main.test")); err != nil {
				t.Fatal(err)
			}
			scan(t, m, root)
			docs = barrier(t, m, root)
			if _, ok := docs[fileURI(filepath.Join(root, "main.test"))]; ok {
				t.Fatal("deleted document stayed open")
			}
			if got := m.Snapshot("first"); len(got) != 1 || got[0].File != "created.test" {
				t.Fatalf("delete retained issues: %+v", got)
			}
			m.mu.Lock()
			runtime := m.roots[root]
			server := runtime.servers["test"]
			m.mu.Unlock()
			m.Detach("first")
			<-runtime.done
			select {
			case <-server.peer.done:
			default:
				t.Fatal("server reader survived detach")
			}
			m.Close()
		})
	}
}
func TestRuntimeRejectsUnmanagedFoldersAndFailedStarts(t *testing.T) {
	m, root := fixtureManager(t, "push")
	for _, dir := range []string{t.TempDir(), filepath.Dir(root)} {
		if err := m.Attach("bad", []string{dir}); err == nil {
			t.Fatalf("accepted %s", dir)
		}
	}
	putFile(t, root, "main.test", "text")
	m.o.Settings = func() api.LSPSettings {
		return api.LSPSettings{Servers: []api.LSPServer{{Name: "missing", Enabled: true, Command: filepath.Join(root, "no-such-server"), Files: []api.LSPFileSelector{{Pattern: "*.test", LanguageID: "test"}}}}}
	}
	if err := m.Attach("first", []string{root}); err != nil {
		t.Fatal(err)
	}
	if err := m.Scan(context.Background(), root); err == nil {
		t.Fatal("failed start not exposed")
	}
	m.Detach("first")
	if got := m.Snapshot("first"); len(got) != 0 {
		t.Fatal(fmt.Sprint(got))
	}
}

func TestRuntimeRegisteredWatchersAndDiskOnlySynchronization(t *testing.T) {
	m, root := fixtureManager(t, "disk")
	putFile(t, root, "main.test", "bad baseline")
	putFile(t, root, "options.cfg", "clean")
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	// The barrier also acknowledges the server's dynamic registration; scan
	// again to cover a registration arriving after initialization completes.
	scan(t, m, root)
	barrier(t, m, root)
	putFile(t, root, "options.cfg", "bad configuration")
	scan(t, m, root)
	barrier(t, m, root)
	got := m.Snapshot("agent")
	found := false
	for _, i := range got {
		if i.File == "options.cfg" && i.Message == "bad configuration" {
			found = true
		}
	}
	if !found {
		t.Fatalf("registered dependency not diagnosed: %+v", got)
	}
}

func TestRuntimePullRefreshUnchangedAndRelatedDocuments(t *testing.T) {
	m, root := fixtureManager(t, "related")
	putFile(t, root, "main.test", "bad baseline")
	putFile(t, root, "related.cfg", "related")
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	if got := m.Snapshot("agent"); len(got) != 2 {
		t.Fatalf("related report lost: %+v", got)
	}
	m.mu.Lock()
	s := m.roots[root].servers["test"]
	m.mu.Unlock()
	if err := s.peer.call(t.Context(), "test/refresh", nil, nil); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	found := false
	for _, i := range m.Snapshot("agent") {
		if i.Message == "bad refresh" {
			found = true
		}
	}
	if !found {
		t.Fatal("refresh request did not pull diagnostics")
	}
	if err := s.peer.call(t.Context(), "test/refresh", nil, nil); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	if len(m.Snapshot("agent")) != 2 {
		t.Fatal("unchanged response cleared previous diagnostics")
	}
}

func TestRuntimeClearsDeletedRelatedFileAndRejectsStalePush(t *testing.T) {
	m, root := fixtureManager(t, "related")
	putFile(t, root, "main.test", "bad baseline")
	putFile(t, root, "related.cfg", "related")
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	if len(m.Snapshot("agent")) != 2 {
		t.Fatal("missing baseline")
	}
	if err := os.Remove(filepath.Join(root, "related.cfg")); err != nil {
		t.Fatal(err)
	}
	putFile(t, root, "main.test", "bad changed")
	scan(t, m, root)
	if got := m.Snapshot("agent"); len(got) != 1 || got[0].File != "main.test" {
		t.Fatalf("deleted related file retained: %+v", got)
	}
}

func TestRuntimeWatchedDependencyTriggersPull(t *testing.T) {
	m, root := fixtureManager(t, "pull-watch")
	putFile(t, root, "main.test", "bad baseline")
	putFile(t, root, "options.cfg", "clean")
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	scan(t, m, root)
	if len(m.Snapshot("agent")) != 1 {
		t.Fatal("missing baseline")
	}
	putFile(t, root, "options.cfg", "bad dependency")
	scan(t, m, root)
	if got := m.Snapshot("agent"); len(got) != 1 || got[0].Message != "bad dependency" {
		t.Fatalf("dependency did not trigger pull: %+v", got)
	}
}

func TestRuntimeDoesNotNotifyBeforeObservedFileChange(t *testing.T) {
	m, root := fixtureManager(t, "push")
	putFile(t, root, "main.test", "bad baseline")
	reports := []Report{}
	unsubscribe := m.Subscribe(func(r Report) { reports = append(reports, r) })
	defer unsubscribe()
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	m.mu.Lock()
	runtime := m.roots[root]
	s := runtime.servers["test"]
	m.mu.Unlock()
	if err := s.peer.call(t.Context(), "test/baselineUpdate", nil, nil); err != nil {
		t.Fatal(err)
	}
	runtime.issues.mu.Lock()
	generation := runtime.issues.generation
	runtime.issues.mu.Unlock()
	runtime.issues.flush(generation)
	if len(reports) != 0 {
		t.Fatalf("initial analysis produced nudges: %+v", reports)
	}
	putFile(t, root, "created.test", "bad new file")
	scan(t, m, root)
	barrier(t, m, root)
	runtime.issues.mu.Lock()
	generation = runtime.issues.generation
	runtime.issues.mu.Unlock()
	runtime.issues.flush(generation)
	if len(reports) != 1 || len(reports[0].Issues) != 1 || reports[0].Issues[0].File != "created.test" {
		t.Fatalf("new file was treated as baseline: %+v", reports)
	}
}

func flushReports(r *worktree) {
	r.issues.mu.Lock()
	generation := r.issues.generation
	r.issues.mu.Unlock()
	r.issues.flush(generation)
}
func TestRuntimeFirstPushAfterEditIsNew(t *testing.T) {
	m, root := fixtureManager(t, "delayed")
	putFile(t, root, "main.test", "clean")
	var reports []Report
	unsubscribe := m.Subscribe(func(r Report) { reports = append(reports, r) })
	defer unsubscribe()
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	putFile(t, root, "main.test", "bad edit before first diagnostics")
	scan(t, m, root)
	barrier(t, m, root)
	m.mu.Lock()
	r := m.roots[root]
	m.mu.Unlock()
	flushReports(r)
	if len(reports) != 1 {
		t.Fatalf("first live push treated as baseline: %+v", reports)
	}
}
func TestRuntimeLateWatcherRegistrationIsNotAnEdit(t *testing.T) {
	m, root := fixtureManager(t, "push")
	putFile(t, root, "main.test", "bad baseline")
	var reports []Report
	unsubscribe := m.Subscribe(func(r Report) { reports = append(reports, r) })
	defer unsubscribe()
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	m.mu.Lock()
	r := m.roots[root]
	s := r.servers["test"]
	m.mu.Unlock()
	_, err := s.handle("client/registerCapability", json.RawMessage(`{"registrations":[{"id":"late","method":"workspace/didChangeWatchedFiles","registerOptions":{"watchers":[{"globPattern":"**/*.test"}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	if err := s.peer.call(t.Context(), "test/baselineUpdate", nil, nil); err != nil {
		t.Fatal(err)
	}
	flushReports(r)
	if len(reports) != 0 {
		t.Fatalf("watcher registration was mistaken for disk edit: %+v", reports)
	}
}
func TestRuntimeLargeWatchedFileDoesNotStopServer(t *testing.T) {
	m, root := fixtureManager(t, "watchall")
	putFile(t, root, "main.test", "bad baseline")
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	scan(t, m, root)
	putFile(t, root, "bundle.bin", strings.Repeat("x", maxDocument+1))
	scan(t, m, root)
	putFile(t, root, "main.test", "bad after large asset")
	scan(t, m, root)
	barrier(t, m, root)
	if got := m.Snapshot("agent"); len(got) != 1 || got[0].Message != "bad after large asset" {
		t.Fatalf("large watched file stopped diagnostics: %+v", got)
	}
}

func TestRuntimeWatcherObservesEditAfterRegistrationBeforeFirstScan(t *testing.T) {
	m, root := fixtureManager(t, "push")
	putFile(t, root, "main.test", "bad baseline")
	putFile(t, root, "options.cfg", "clean")
	var reports []Report
	unsubscribe := m.Subscribe(func(r Report) { reports = append(reports, r) })
	defer unsubscribe()
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	m.mu.Lock()
	r := m.roots[root]
	s := r.servers["test"]
	m.mu.Unlock()
	_, err := s.handle("client/registerCapability", json.RawMessage(`{"registrations":[{"id":"late","method":"workspace/didChangeWatchedFiles","registerOptions":{"watchers":[{"globPattern":"**/*.{test,cfg}"}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, root, "options.cfg", "new dependency contents")
	scan(t, m, root)
	barrier(t, m, root)
	if err := s.peer.call(t.Context(), "test/baselineUpdate", nil, nil); err != nil {
		t.Fatal(err)
	}
	flushReports(r)
	if len(reports) != 1 {
		t.Fatalf("post-registration edit was swallowed: %+v", reports)
	}
}

func TestRuntimeSkipsOversizedSelectedFileWithoutBlockingOthers(t *testing.T) {
	m, root := fixtureManager(t, "push")
	var logs bytes.Buffer
	m.o.Log = log.New(&logs, "", 0)
	putFile(t, root, "too-large.test", strings.Repeat("x", maxDocument+1))
	putFile(t, root, "normal.test", "bad baseline")
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	if got := m.Snapshot("agent"); len(got) != 1 || got[0].File != "normal.test" {
		t.Fatalf("large file blocked normal diagnostics: %+v", got)
	}
	if !strings.Contains(logs.String(), "too-large.test") {
		t.Fatalf("skipped file was not logged: %s", logs.String())
	}
	putFile(t, root, "normal.test", "bad updated")
	scan(t, m, root)
	barrier(t, m, root)
	if got := m.Snapshot("agent"); len(got) != 1 || got[0].Message != "bad updated" {
		t.Fatalf("updates stopped: %+v", got)
	}
	putFile(t, root, "too-large.test", "bad recovered")
	scan(t, m, root)
	barrier(t, m, root)
	if len(m.Snapshot("agent")) != 2 {
		t.Fatal("skipped file did not recover")
	}
}

func TestRuntimeRetriesPullErrorWithoutStoppingServer(t *testing.T) {
	for _, mode := range []string{"pull-error", "workspace-error"} {
		t.Run(mode, func(t *testing.T) {
			m, root := fixtureManager(t, mode)
			putFile(t, root, "main.test", "bad baseline")
			if err := m.Attach("agent", []string{root}); err != nil {
				t.Fatal(err)
			}
			// Initial startup consumes the one error. An explicit following tick must
			// retry despite unchanged files, preserving the same live connection.
			scan(t, m, root)
			barrier(t, m, root)
			if got := m.Snapshot("agent"); len(got) != 1 || got[0].Message != "bad baseline" {
				t.Fatalf("pull did not recover: %+v", got)
			}
			putFile(t, root, "main.test", "bad updated")
			scan(t, m, root)
			if got := m.Snapshot("agent"); len(got) != 1 || got[0].Message != "bad updated" {
				t.Fatalf("server stopped after error: %+v", got)
			}
		})
	}
}

func TestRuntimeStderrIsNotAnInheritedCopyPipe(t *testing.T) {
	m, root := fixtureManager(t, "stderr")
	putFile(t, root, "main.test", "source")
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	if got := m.Snapshot("agent"); len(got) != 1 || got[0].Message != "bad stderr file" {
		t.Fatalf("stderr can hold Wait open: %+v", got)
	}
	m.Detach("agent")
}

func TestRuntimeDropsStoppedServers(t *testing.T) {
	m, root := fixtureManager(t, "push")
	putFile(t, root, "main.test", "bad baseline")
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	m.mu.Lock()
	r := m.roots[root]
	s := r.servers["test"]
	m.mu.Unlock()
	_ = s.peer.notify("test/exit", nil)
	<-s.waited
	if err := m.Scan(t.Context(), root); err == nil {
		t.Fatal("server exit was not reported")
	}
	if len(r.servers) != 0 {
		t.Fatal("stopped server retained for later signaling")
	}
	if err := m.Scan(t.Context(), root); err == nil {
		t.Fatal("failed registration was retried without a config change")
	}
}

func TestRuntimeDependencyEditKeepsUnreportedDocumentBaseline(t *testing.T) {
	m, root := fixtureManager(t, "delayed")
	putFile(t, root, "main.test", "clean")
	putFile(t, root, "options.cfg", "clean")
	var reports []Report
	unsubscribe := m.Subscribe(func(r Report) { reports = append(reports, r) })
	defer unsubscribe()
	if err := m.Attach("agent", []string{root}); err != nil {
		t.Fatal(err)
	}
	scan(t, m, root)
	barrier(t, m, root)
	m.mu.Lock()
	r := m.roots[root]
	s := r.servers["test"]
	m.mu.Unlock()
	_, err := s.handle("client/registerCapability", json.RawMessage(`{"registrations":[{"id":"config","method":"workspace/didChangeWatchedFiles","registerOptions":{"watchers":[{"globPattern":"**/*.cfg"}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, root, "options.cfg", "changed dependency")
	scan(t, m, root)
	if err := s.peer.call(t.Context(), "test/baselineUpdate", nil, nil); err != nil {
		t.Fatal(err)
	}
	flushReports(r)
	if len(reports) != 0 {
		t.Fatalf("untouched unreported document lost its baseline: %+v", reports)
	}
}
