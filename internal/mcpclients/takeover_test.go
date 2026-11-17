package mcpclients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// fixtureSecrets are the secret values of the fixtures.
var fixtureSecrets = []string{
	"claude-tracker-secret-7f3a", "claude-wiki-secret-4d2e", "claude-events-secret-91bc", "claude-search-secret-c0de",
	"claude-db-secret-77aa", "codex-search-secret-5e11", "platform-codex-secret-1b1b", "codex-calendar-secret-aa42",
	"codex-docs-secret-d0c5", "codex-legacy-secret-0046", "agy-boards-secret-b0a7", "agy-maps-secret-3c3c",
}

// fixtureTargets puts every fixture in place: Claude's and Codex's default
// files, agy's file and an old Codex file in an account folder.
func fixtureTargets(t *testing.T, f *fakes) []Target {
	t.Helper()
	acc := filepath.Join(f.home, "accounts", "old-codex")
	targets := []Target{
		target(t, f, api.CLIClaude, ""), target(t, f, api.CLICodex, ""),
		target(t, f, api.CLICodex, acc), target(t, f, api.CLIAgy, ""),
	}
	put(t, "claude-2.1.326.json", targets[0].ConfigPath)
	put(t, "codex-0.162.1-config.toml", targets[1].ConfigPath)
	put(t, "codex-0.46.0-config.toml", targets[2].ConfigPath)
	put(t, "agy-1.5.9-mcp_config.json", targets[3].ConfigPath)
	return targets
}

// existing are the servers omo has: one named like a Claude entry with
// another address, one equal to Codex's Work_Notes.
var existing = []api.MCPServer{
	{Name: "events", Transport: api.MCPTransportHTTP, URL: "https://other-events.example.invalid/mcp"},
	{Name: "notes", Transport: api.MCPTransportStdio, Command: "notes-mcp", Cwd: "/srv/notes"},
}

func candidate(t *testing.T, tk api.MCPTakeover, name string) api.MCPTakeoverCandidate {
	t.Helper()
	for _, c := range tk.Candidates {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no candidate %s in %v", name, names(tk))
	return api.MCPTakeoverCandidate{}
}

func names(tk api.MCPTakeover) []string {
	var out []string
	for _, c := range tk.Candidates {
		out = append(out, c.Name)
	}
	return out
}

func TestTakeoverPreview(t *testing.T) {
	f := newFakes(t)
	targets := fixtureTargets(t, f)
	tk := f.clients.Preview(targets, existing)

	// Keys depend on nothing temporary, but target ids of account folders
	// do: name the account target's id for the golden file.
	shown := MaskTakeover(tk)
	for i := range shown.Candidates {
		for j := range shown.Candidates[i].Sources {
			if s := &shown.Candidates[i].Sources[j]; s.Target == targets[2].ID {
				s.Target = "codex-<hash of the account>"
			}
		}
	}
	for i := range shown.Stays {
		if shown.Stays[i].Target == targets[2].ID {
			shown.Stays[i].Target = "codex-<hash of the account>"
		}
	}
	golden(t, f, "takeover-preview", shown)

	// Per file in the order of the names; Claude's, then Codex's files,
	// then agy's.
	want := []string{"search-index", "calendar", "docs", "events-claude", "filesystem", "future", "rotating", "tracker", "wiki",
		"work-notes", "calendar-work", "docs-codex", "issues", "search", "tracker-codex", "legacy", "slowstart", "boards", "local", "maps"}
	if got := names(tk); !slices.Equal(got, want) {
		t.Fatalf("candidates\n%v\nwant\n%v", got, want)
	}
	// The same server with the same values in three files is one
	// candidate.
	fs := candidate(t, tk, "filesystem")
	if len(fs.Sources) != 3 || fs.Server.Source != "claude, codex, agy" || fs.Conflict != "" {
		t.Fatalf("filesystem %+v", fs)
	}
	// Every candidate is global; OAuth ones need a login.
	for _, c := range tk.Candidates {
		if !c.Server.Global {
			t.Errorf("%s is not global", c.Name)
		}
		if c.NeedsLogin != (c.Server.Auth == api.MCPAuthOAuth) {
			t.Errorf("%s: needs_login %v with auth %s", c.Name, c.NeedsLogin, c.Server.Auth)
		}
	}
	for _, n := range []string{"calendar", "calendar-work", "docs", "maps"} {
		if !candidate(t, tk, n).NeedsLogin {
			t.Errorf("%s does not need a login", n)
		}
	}
	if c := candidate(t, tk, "work-notes"); !strings.Contains(c.Conflict, "already has this server as notes") {
		t.Errorf("work-notes conflict %q", c.Conflict)
	}
	if c := candidate(t, tk, "tracker-codex"); c.Server.Headers["Authorization"] != "Bearer ${TRACKER_TOKEN}" {
		t.Errorf("tracker-codex headers %v", c.Server.Headers)
	}
	if c := candidate(t, tk, "legacy"); c.Server.Headers["Authorization"] != "Bearer codex-legacy-secret-0046" || c.Server.Auth != api.MCPAuthHeaders {
		t.Errorf("legacy %+v", c.Server)
	}
	if c := candidate(t, tk, "search"); c.Server.Env["SEARCH_REGION"] != "${SEARCH_REGION}" || c.Server.Timeouts.Call != api.Duration(120e9) {
		t.Errorf("search %+v", c.Server)
	}
	var stays []string
	for _, s := range tk.Stays {
		stays = append(stays, s.EntryName)
	}
	if !slices.Equal(stays, []string{"chat", "app-db", "-x", "drive"}) {
		t.Fatalf("stays %v", stays)
	}
}

// TestPreviewShowsNoValues is the guard for the preview: the masked
// preview and states hold no value of the files, but their keys.
func TestPreviewShowsNoValues(t *testing.T) {
	f := newFakes(t)
	targets := fixtureTargets(t, f)
	raw, _ := json.Marshal(f.clients.Preview(targets, nil))
	masked, _ := json.Marshal(MaskTakeover(f.clients.Preview(targets, nil)))
	var states []api.MCPClientState
	for _, tg := range targets {
		states = append(states, MaskState(f.clients.Inspect(tg)))
	}
	shownStates, _ := json.Marshal(states)
	for _, s := range fixtureSecrets {
		if bytes.Contains(masked, []byte(s)) || bytes.Contains(shownStates, []byte(s)) {
			t.Errorf("%s is shown", s)
		}
	}
	// The values are there before masking (the test checks something),
	// and the keys stay visible.
	if !bytes.Contains(raw, []byte("claude-search-secret-c0de")) || !bytes.Contains(masked, []byte("SEARCH_TOKEN")) {
		t.Fatal("the unmasked preview lacks a value or the masked one a key")
	}
}

// memStore is omo's settings store for the take-over; it records each
// write in the fake programs' log, so that the order of writes and
// removals can be checked.
type memStore struct {
	t       *testing.T
	log     string
	servers []api.MCPServer
	fail    error
}

func (s *memStore) MCPServers() []api.MCPServer { return slices.Clone(s.servers) }

func (s *memStore) PutMCPServerIf(rev string, srv api.MCPServer) error {
	if s.fail != nil {
		return s.fail
	}
	if rev != api.NoRevision {
		s.t.Fatalf("the take-over wrote with revision %q, not create-only", rev)
	}
	if slices.ContainsFunc(s.servers, func(x api.MCPServer) bool { return x.Name == srv.Name }) {
		return api.Errorf(api.CodeStaleSettings, "mcp server %s exists", srv.Name)
	}
	s.servers = append(s.servers, srv)
	line, _ := json.Marshal(call{CLI: "store", Args: []string{srv.Name}})
	fh, err := os.OpenFile(s.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer fh.Close()
	_, err = fh.Write(append(line, '\n'))
	return err
}

func pick(t *testing.T, tk api.MCPTakeover, names ...string) []api.MCPTakeoverPick {
	t.Helper()
	var out []api.MCPTakeoverPick
	for _, n := range names {
		out = append(out, api.MCPTakeoverPick{Key: candidate(t, tk, n).Key, Name: n})
	}
	return out
}

func TestApplyTakeoverStoresBeforeRemoving(t *testing.T) {
	f := newFakes(t)
	targets := fixtureTargets(t, f)
	originals := map[string][]byte{}
	for _, tg := range targets {
		originals[tg.ID], _ = os.ReadFile(tg.ConfigPath)
	}
	store := &memStore{t: t, log: filepath.Join(f.bin, "calls.log")}
	tk := f.clients.Preview(targets, store.MCPServers())
	report := f.clients.ApplyTakeover(context.Background(), targets,
		api.MCPTakeoverApply{Take: pick(t, tk, "filesystem", "search-index", "calendar"), RemoveFromCLI: true}, store)

	for _, r := range report.Results {
		if !r.Stored || r.Error != "" {
			t.Fatalf("%s: %+v", r.Name, r)
		}
		for _, rm := range r.Removed {
			if !rm.Done {
				t.Fatalf("%s from %s: %s", r.Name, rm.Target, rm.Error)
			}
		}
	}
	if fs := report.Results[0]; len(fs.Removed) != 3 {
		t.Fatalf("filesystem was removed from %d files, want 3", len(fs.Removed))
	}
	if !report.Results[2].NeedsLogin {
		t.Fatal("calendar does not report needs_login")
	}

	// Order: omo's copy of each server is written before any removal of
	// its entries.
	stored := map[string]int{}
	for i, c := range f.calls(t) {
		if c.CLI == "store" {
			stored[c.Args[0]] = i
			continue
		}
		entry := c.Args[len(c.Args)-1]
		name := map[string]string{"filesystem": "filesystem", "Search Index": "search-index", "calendar": "calendar"}[entry]
		at, ok := stored[name]
		if !ok || at > i {
			t.Fatalf("%s was removed from %s before omo stored %s", entry, c.CLI, name)
		}
	}
	// omo has the values, global, with their source.
	srv := store.servers[1]
	if srv.Name != "search-index" || srv.Env["SEARCH_TOKEN"] != "claude-search-secret-c0de" || !srv.Global || srv.Source != "claude" {
		t.Fatalf("stored %+v", srv)
	}
	// The entries are gone from the files, the rest is kept.
	for _, tg := range targets {
		for _, e := range f.clients.Inspect(tg).Entries {
			if slices.Contains([]string{"filesystem", "Search Index", "calendar"}, e.Name) {
				t.Errorf("%s is still in %s", e.Name, tg.ID)
			}
		}
	}
	if st := f.clients.Inspect(targets[0]); len(st.Entries) != 8 {
		t.Errorf("claude has %d entries left, want 8", len(st.Entries))
	}
	// One backup per changed file, as it was before.
	if len(report.Backups) != 3 {
		t.Fatalf("backups %v", report.Backups)
	}
	for _, b := range report.Backups {
		id := strings.SplitN(filepath.Base(b), "-", 2)[0]
		if strings.HasPrefix(filepath.Base(b), "codex-") && !strings.HasSuffix(b, "-config.toml") {
			t.Fatalf("backup name %s", b)
		}
		data, _ := os.ReadFile(b)
		if !bytes.Equal(data, originals[id]) {
			t.Errorf("backup %s is not the file before", b)
		}
		if !strings.Contains(b, "-mcp-takeover") {
			t.Errorf("backup %s is not in a take-over folder", b)
		}
	}
}

func TestApplyTakeoverWithoutStoredCopyRemovesNothing(t *testing.T) {
	f := newFakes(t)
	targets := fixtureTargets(t, f)
	before, _ := os.ReadFile(targets[0].ConfigPath)
	store := &memStore{t: t, log: filepath.Join(f.bin, "calls.log"),
		fail: errors.New("disk full while writing SEARCH_TOKEN=claude-search-secret-c0de")}
	tk := f.clients.Preview(targets, nil)
	report := f.clients.ApplyTakeover(context.Background(), targets,
		api.MCPTakeoverApply{Take: pick(t, tk, "search-index"), RemoveFromCLI: true}, store)
	r := report.Results[0]
	if r.Stored || len(r.Removed) != 0 || !strings.Contains(r.Error, "stays in the program") {
		t.Fatalf("result %+v", r)
	}
	if strings.Contains(r.Error, "claude-search-secret-c0de") {
		t.Fatalf("the error shows a value: %s", r.Error)
	}
	if len(f.calls(t)) != 0 || len(report.Backups) != 0 {
		t.Fatal("a program ran or a backup was made although nothing was stored")
	}
	after, _ := os.ReadFile(targets[0].ConfigPath)
	if !bytes.Equal(before, after) {
		t.Fatal("the program's file changed")
	}
}

func TestApplyTakeoverFailedRemovalKeepsTheEntry(t *testing.T) {
	f := newFakes(t)
	targets := fixtureTargets(t, f)
	f.fail(t, "remove", "error: config locked; last read Bearer claude-tracker-secret-7f3a")
	store := &memStore{t: t, log: filepath.Join(f.bin, "calls.log")}
	tk := f.clients.Preview(targets, nil)
	report := f.clients.ApplyTakeover(context.Background(), targets,
		api.MCPTakeoverApply{Take: pick(t, tk, "tracker"), RemoveFromCLI: true}, store)
	r := report.Results[0]
	if !r.Stored || len(r.Removed) != 1 || r.Removed[0].Done {
		t.Fatalf("result %+v", r)
	}
	msg := r.Removed[0].Error
	if !strings.HasPrefix(msg, "still in claude: ") || !strings.Contains(msg, "remove it by hand or retry") || strings.Contains(msg, "claude-tracker-secret-7f3a") {
		t.Fatalf("removal error %q", msg)
	}
	// omo has its copy, the program still has its entry, and the next
	// preview shows it as a server omo already has.
	if len(store.servers) != 1 {
		t.Fatal("omo's copy is missing")
	}
	if !slices.ContainsFunc(f.clients.Inspect(targets[0]).Entries, func(e api.MCPClientEntry) bool { return e.Name == "tracker" }) {
		t.Fatal("the entry is gone from the program's file")
	}
	again := f.clients.Preview(targets, store.MCPServers())
	if c := candidate(t, again, "tracker"); !strings.Contains(c.Conflict, "already has this server as tracker") {
		t.Fatalf("the next preview: %+v", c)
	}
	if len(report.Backups) != 1 {
		t.Fatalf("backups %v", report.Backups)
	}
}

func TestApplyTakeoverWithoutRemoval(t *testing.T) {
	f := newFakes(t)
	targets := fixtureTargets(t, f)
	store := &memStore{t: t, log: filepath.Join(f.bin, "calls.log")}
	tk := f.clients.Preview(targets, nil)
	report := f.clients.ApplyTakeover(context.Background(), targets,
		api.MCPTakeoverApply{Take: pick(t, tk, "boards"), RemoveFromCLI: false}, store)
	if r := report.Results[0]; !r.Stored || len(r.Removed) != 0 {
		t.Fatalf("result %+v", r)
	}
	if n := len(f.calls(t)); n != 1 { // the store's line only
		t.Fatalf("%d calls", n)
	}
	if s := store.servers[0]; s.Enabled || s.Timeouts.Call != api.Duration(300e9) || !slices.Equal(s.Tools.Allow, []string{"list_boards"}) {
		t.Fatalf("boards %+v", s)
	}
}

func TestApplyTakeoverRefusesBadPicks(t *testing.T) {
	f := newFakes(t)
	targets := fixtureTargets(t, f)
	store := &memStore{t: t, log: filepath.Join(f.bin, "calls.log")}
	tk := f.clients.Preview(targets, nil)
	key := candidate(t, tk, "wiki").Key
	report := f.clients.ApplyTakeover(context.Background(), targets, api.MCPTakeoverApply{Take: []api.MCPTakeoverPick{
		{Key: "k0000000000000000", Name: "gone"},
		{Key: key, Name: "Bad Name"},
		{Key: key, Name: "wiki"},
		{Key: candidate(t, tk, "docs").Key, Name: "wiki"},
	}, RemoveFromCLI: true}, store)
	errs := []string{"review again", "the name must be", "", "chosen twice"}
	for i, r := range report.Results {
		if errs[i] == "" {
			if !r.Stored {
				t.Errorf("pick %d: %+v", i, r)
			}
			continue
		}
		if r.Stored || !strings.Contains(r.Error, errs[i]) {
			t.Errorf("pick %d: %+v", i, r)
		}
	}
	if len(store.servers) != 1 {
		t.Fatalf("stored %d servers", len(store.servers))
	}
}

func TestProjectStays(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".mcp.json"), `{"mcpServers":{"repo-db":{"command":"db","env":{"P":"project-secret-9999"}}}}`)
	writeFile(t, filepath.Join(dir, ".codex", "config.toml"), "[mcp_servers.repo-lint]\ncommand = \"lint\"\n")
	writeFile(t, filepath.Join(dir, ".agents", "mcp_config.json"), `{"mcpServers":{"repo-docs":{"serverUrl":"https://d.example.invalid"}}}`)
	got := ProjectStays([]string{dir, filepath.Join(dir, "missing")})
	var entries []string
	for _, s := range got {
		entries = append(entries, s.EntryName)
		if !strings.Contains(s.Reason, "stays there") {
			t.Errorf("reason %q", s.Reason)
		}
	}
	if !slices.Equal(entries, []string{"repo-db", "repo-lint", "repo-docs"}) {
		t.Fatalf("stays %v", entries)
	}
	if data, _ := json.Marshal(got); bytes.Contains(data, []byte("project-secret-9999")) {
		t.Fatal("a stay shows a value")
	}
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{
		"Search Index":                       "search-index",
		"Work_Notes":                         "work-notes",
		"1password":                          "mcp-1password",
		"__":                                 "mcp",
		"a-very-long-server-name-that-keeps": "a-very-long-server-name",
		"github":                             "github",
	} {
		if got := normalizeName(in); got != want || !namePattern.MatchString(got) {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := withSuffix("a-very-long-server-name", "claude"); got != "a-very-long-serve-claude" || !namePattern.MatchString(got) {
		t.Errorf("withSuffix: %q", got)
	}
}
