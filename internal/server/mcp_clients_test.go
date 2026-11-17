package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcpclients"
)

func init() {
	// The agent program routes are guarded: each must be in the route
	// table with its rule.
	guardedPrefixes = append(guardedPrefixes, "/api/mcp/clients", "/api/mcp/takeover")
	// No test may reach the user's real home or real agent programs
	// through these routes; a test that needs them gives a temporary
	// machine (mcpTestMachine).
	mcpClientsMachine = func(*Server) (mcpclients.Options, error) {
		return mcpclients.Options{}, api.Errorf(api.CodeUnavailable, "tests give the agent program routes a temporary machine")
	}
}

// mcpClientRoutes are the agent program routes with a body each accepts.
var mcpClientRoutes = []extensionRoute{
	{"GET /api/mcp/clients", nil},
	{"POST /api/mcp/clients/{target}/plan", api.MCPClientPlanRequest{Action: api.MCPClientInstall}},
	{"POST /api/mcp/clients/{target}/apply", api.MCPClientApply{Plan: "x"}},
	{"GET /api/mcp/takeover", nil},
	{"POST /api/mcp/takeover/apply", api.MCPTakeoverApply{}},
}

// TestMCPClientRoutesAreUserOnly: the routes are in the table as user
// only, and an agent session gets 403 from each before anything is read or
// run.
func TestMCPClientRoutesAreUserOnly(t *testing.T) {
	s := newTestServer(t)
	m := mcpTestMachine(t, s)
	sess := liveSession(t, s, api.RoleDeveloper)
	for _, r := range mcpClientRoutes {
		t.Run(r.pattern, func(t *testing.T) {
			found := false
			for _, row := range s.routeTable {
				if row.pattern == r.pattern {
					found = true
					if row.access != userOnly {
						t.Fatalf("%s is %s", r.pattern, row.access)
					}
				}
			}
			if !found {
				t.Fatalf("%s is not in the route table", r.pattern)
			}
			method, path, _ := strings.Cut(r.pattern, " ")
			rec := socketDo(t, s, method, strings.ReplaceAll(path, "{target}", "agy"), r.body, sess.ID)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("an agent got %d, want 403", rec.Code)
			}
		})
	}
	if data, _ := os.ReadFile(m.log); len(data) != 0 {
		t.Fatalf("a program ran for an agent: %s", data)
	}
}

// mcpMachine is a temporary machine for the agent program routes: a home
// with agy's fixture file and a fake agy.
type mcpMachine struct {
	home, bin, log, file string
	omo                  string
}

const mcpTestSecret = "agy-boards-secret-b0a7"

// mcpTestMachine gives s a temporary home with agy's fixture and a fake
// agy: "mcp add" puts omo's bridge into the file, "mcp remove" puts the
// fixture back, and a file "fail-remove" next to it makes "mcp remove"
// fail, printing a value of the file.
func mcpTestMachine(t *testing.T, s *Server) *mcpMachine {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent program is an sh script")
	}
	root := t.TempDir()
	m := &mcpMachine{home: filepath.Join(root, "home"), bin: filepath.Join(root, "bin"), omo: filepath.Join(root, "omo-bin", "omo")}
	m.log = filepath.Join(m.bin, "calls.log")
	m.file = filepath.Join(m.home, ".gemini", "config", "mcp_config.json")
	fixture, err := os.ReadFile(filepath.Join("..", "mcpclients", "testdata", "agy-1.5.9-mcp_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]map[string]any
	if err := json.Unmarshal(fixture, &top); err != nil {
		t.Fatal(err)
	}
	top["mcpServers"]["omo"] = map[string]any{"command": m.omo, "args": []string{"mcp", "stdio"}}
	installed, _ := json.Marshal(top)
	for path, data := range map[string][]byte{
		m.file:                             fixture,
		filepath.Join(m.bin, "original"):   fixture,
		filepath.Join(m.bin, "installed"):  installed,
		filepath.Join(m.bin, "agy-script"): nil,
	} {
		os.MkdirAll(filepath.Dir(path), 0o700)
		if data != nil {
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	script := `#!/bin/sh
echo "$@" >> '` + m.log + `'
case "$2" in
add) cp '` + filepath.Join(m.bin, "installed") + `' "$HOME/.gemini/config/mcp_config.json" ;;
remove)
	if [ -f '` + filepath.Join(m.bin, "fail-remove") + `' ]; then echo "cannot remove: Bearer ` + mcpTestSecret + `" >&2; exit 1; fi
	cp '` + filepath.Join(m.bin, "original") + `' "$HOME/.gemini/config/mcp_config.json" ;;
esac
`
	if err := os.WriteFile(filepath.Join(m.bin, "agy"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	old := mcpClientsMachine
	mcpClientsMachine = func(*Server) (mcpclients.Options, error) {
		return mcpclients.Options{
			Home: m.home, OmoPath: m.omo, OmoHome: s.opts.Layout.Root,
			Env: []string{"HOME=" + m.home, "PATH=/usr/bin:/bin"},
			LookPath: func(name string) (string, error) {
				return filepath.Join(m.bin, filepath.Base(name)), nil
			},
		}, nil
	}
	t.Cleanup(func() { mcpClientsMachine = old })
	return m
}

func userJSON[T any](t *testing.T, s *Server, method, path string, body any, want int) T {
	t.Helper()
	rec := socketDo(t, s, method, path, body, "")
	if rec.Code != want {
		t.Fatalf("%s %s: %d %s, want %d", method, path, rec.Code, rec.Body.String(), want)
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMCPClientsInstallAndRemove(t *testing.T) {
	var logs syncBuffer
	s, err := newServer(Options{Layout: testHome(t), Config: testConfig(), Version: "test"}, &logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m := mcpTestMachine(t, s)

	states := userJSON[[]api.MCPClientState](t, s, "GET", "/api/mcp/clients", nil, http.StatusOK)
	if len(states) != 1 || states[0].Target.ID != "agy" || states[0].State != api.MCPClientAbsent {
		t.Fatalf("states %+v", states)
	}
	raw, _ := json.Marshal(states)
	if strings.Contains(string(raw), mcpTestSecret) || !strings.Contains(string(raw), "Authorization") {
		t.Fatalf("the list shows a value or hides a key: %s", raw)
	}

	plan := userJSON[api.MCPClientPlan](t, s, "POST", "/api/mcp/clients/agy/plan", api.MCPClientPlanRequest{Action: api.MCPClientInstall}, http.StatusOK)
	if len(plan.Commands) != 1 || plan.Commands[0][len(plan.Commands[0])-3] != m.omo {
		t.Fatalf("plan %+v", plan)
	}
	applied := userJSON[api.MCPClientApplied](t, s, "POST", "/api/mcp/clients/agy/apply", api.MCPClientApply{Plan: plan.Hash}, http.StatusOK)
	if applied.State.State != api.MCPClientInstalled || !strings.HasPrefix(applied.Backup, filepath.Join(s.opts.Layout.Root, "backups")) {
		t.Fatalf("applied %+v", applied)
	}
	// The plan is spent: the file changed.
	rec := socketDo(t, s, "POST", "/api/mcp/clients/agy/apply", api.MCPClientApply{Plan: plan.Hash}, "")
	wantStatus(t, rec.Code, http.StatusConflict)

	plan = userJSON[api.MCPClientPlan](t, s, "POST", "/api/mcp/clients/agy/plan", api.MCPClientPlanRequest{Action: api.MCPClientRemove}, http.StatusOK)
	applied = userJSON[api.MCPClientApplied](t, s, "POST", "/api/mcp/clients/agy/apply", api.MCPClientApply{Plan: plan.Hash}, http.StatusOK)
	if applied.State.State != api.MCPClientAbsent {
		t.Fatalf("after the removal %+v", applied)
	}
	rec = socketDo(t, s, "POST", "/api/mcp/clients/nope/plan", api.MCPClientPlanRequest{Action: api.MCPClientInstall}, "")
	wantStatus(t, rec.Code, http.StatusNotFound)
	if calls, _ := os.ReadFile(m.log); strings.Count(string(calls), "\n") != 2 {
		t.Fatalf("calls %s", calls)
	}
	if strings.Contains(logs.String(), mcpTestSecret) {
		t.Fatal("the log holds a value of the file")
	}
}

// memMCPStore stands in for omo's store of relayed servers.
type memMCPStore struct{ servers []api.MCPServer }

func (m *memMCPStore) MCPServers() []api.MCPServer { return slices.Clone(m.servers) }
func (m *memMCPStore) PutMCPServerIf(rev string, srv api.MCPServer) error {
	if rev != api.NoRevision {
		return api.Errorf(api.CodeBadRequest, "not create-only")
	}
	m.servers = append(m.servers, srv)
	return nil
}

func TestMCPTakeoverRoutes(t *testing.T) {
	var logs syncBuffer
	s, err := newServer(Options{Layout: testHome(t), Config: testConfig(), Version: "test"}, &logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m := mcpTestMachine(t, s)
	repo := newRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".mcp.json"), []byte(`{"mcpServers":{"repo-db":{"command":"db"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.settings.AddProject(repo, ""); err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())

	// Without omo's store of relayed servers the apply is unavailable.
	if _, ok := mcpServerStore(s); !ok {
		rec := socketDo(t, s, "POST", "/api/mcp/takeover/apply", api.MCPTakeoverApply{}, "")
		wantStatus(t, rec.Code, http.StatusServiceUnavailable)
	}
	store := &memMCPStore{}
	old := mcpServerStore
	mcpServerStore = func(*Server) (mcpclients.Store, bool) { return store, true }
	t.Cleanup(func() { mcpServerStore = old })

	tk := userJSON[api.MCPTakeover](t, s, "GET", "/api/mcp/takeover", nil, http.StatusOK)
	raw, _ := json.Marshal(tk)
	if strings.Contains(string(raw), mcpTestSecret) {
		t.Fatal("the preview shows a value")
	}
	var boards api.MCPTakeoverCandidate
	for _, c := range tk.Candidates {
		if c.Name == "boards" {
			boards = c
		}
	}
	if boards.Key == "" || !boards.Server.Global {
		t.Fatalf("boards %+v", boards)
	}
	if !slices.ContainsFunc(tk.Stays, func(st api.MCPTakeoverStay) bool { return st.EntryName == "repo-db" }) ||
		!slices.ContainsFunc(tk.Stays, func(st api.MCPTakeoverStay) bool { return st.EntryName == "drive" }) {
		t.Fatalf("stays %+v", tk.Stays)
	}

	// The removal fails: omo keeps its copy, the report says it is still
	// in agy, and no value reaches the answer or the log.
	os.WriteFile(filepath.Join(m.bin, "fail-remove"), nil, 0o600)
	report := userJSON[api.MCPTakeoverReport](t, s, "POST", "/api/mcp/takeover/apply",
		api.MCPTakeoverApply{Take: []api.MCPTakeoverPick{{Key: boards.Key, Name: "boards"}}, RemoveFromCLI: true}, http.StatusOK)
	r := report.Results[0]
	if !r.Stored || len(r.Removed) != 1 || r.Removed[0].Done || !strings.HasPrefix(r.Removed[0].Error, "still in agy") {
		t.Fatalf("report %+v", report)
	}
	if len(store.servers) != 1 || store.servers[0].Headers["Authorization"] != "Bearer "+mcpTestSecret || !store.servers[0].Global {
		t.Fatalf("stored %+v", store.servers)
	}
	reportJSON, _ := json.Marshal(report)
	if strings.Contains(string(reportJSON), mcpTestSecret) || strings.Contains(logs.String(), mcpTestSecret) {
		t.Fatal("a value reached the answer or the log")
	}
	if !strings.Contains(logs.String(), "take-over of boards") {
		t.Fatalf("log %q", logs.String())
	}
	if len(report.Backups) != 1 {
		t.Fatalf("backups %v", report.Backups)
	}
}
