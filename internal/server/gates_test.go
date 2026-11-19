package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

// gateLua is the gate of the sample plugin. Its answer follows the ticket's
// title, so one plugin shows every kind of answer.
const gateLua = `
local title = event.data.ticket.title
assert(event.data.move == "release" or event.data.move == "start")
assert(type(event.data.projects) == "table" and event.data.type == "task")
if title:find("hold") then
  return {action = "hold", reason = "waiting for " .. config.who,
          choices = {{label = "Go on", action = "go-on"}, {label = "Stop", action = "stop"}}}
end
if title:find("refuse") then return {action = "refuse", reason = "not now"} end
if title:find("crash") then error("the gate fell over") end
if title:find("spin") then while true do end end
if title:find("garbage") then return 42 end
return {action = "go_ahead"}
`

// gatePluginRepo is a local repository of a plugin that gates the moves in
// moves with gateLua and offers the manual hooks go-on (go ahead) and stop
// (cancel).
func gatePluginRepo(t *testing.T, name string, moves []string, gateExtra map[string]any) string {
	t.Helper()
	hooks := []any{
		map[string]any{"event": "manual", "name": "go-on", "description": "Go ahead", "scope": "ticket", "lua": "go-on.lua"},
		map[string]any{"event": "manual", "name": "stop", "description": "Cancel the move", "scope": "ticket", "lua": "stop.lua"},
	}
	for _, move := range moves {
		h := map[string]any{"event": plugins.EventGate, "move": move, "lua": "gate.lua"}
		for k, v := range gateExtra {
			h[k] = v
		}
		hooks = append(hooks, h)
	}
	manifest := map[string]any{"name": name, "version": "1.0.0", "description": "gates moves", "default_config": map[string]any{"who": "you"}, "hooks": hooks}
	work := t.TempDir()
	bare := filepath.Join(t.TempDir(), "plugin.git")
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"plugin.json": string(raw),
		"gate.lua":    gateLua,
		"go-on.lua":   "assert(omo.gate_release(event.data.ticket.number))\nreturn 'released'\n",
		"stop.lua":    "assert(omo.gate_cancel(event.data.ticket.number))\nreturn 'cancelled'\n",
	}
	for file, text := range files {
		if err := os.WriteFile(filepath.Join(work, file), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run(work, "init", "-q", "-b", "main")
	run(work, "add", ".")
	run(work, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "plugin")
	run(work, "clone", "-q", "--bare", work, bare)
	return bare
}

// gateServer is an engine server (not started) with a browser.
type gateServer struct {
	t      *testing.T
	s      *Server
	b      *browser
	runner *testRunner
	repos  map[string]string
}

func newGateServer(t *testing.T, startEngine bool) *gateServer {
	t.Helper()
	runner := &testRunner{}
	s, _ := engineServer(t, runner, fakeProfile(), startEngine)
	return &gateServer{t: t, s: s, b: newBrowser(t, s), runner: runner, repos: map[string]string{}}
}

func (g *gateServer) install(name string, moves []string, gateExtra map[string]any) {
	g.t.Helper()
	repo := gatePluginRepo(g.t, name, moves, gateExtra)
	g.repos[name] = repo
	resp := asUser(g.b, g.s, "POST", "/api/plugins", community(api.PluginSettings{Name: name, Source: repo, Enabled: true}))
	if resp.StatusCode != http.StatusCreated {
		g.t.Fatalf("install %s: status %d: %s", name, resp.StatusCode, respText(g.t, resp))
	}
}

func (g *gateServer) setEnabled(name string, on bool) {
	g.t.Helper()
	resp := asUser(g.b, g.s, "PUT", "/api/plugins/"+name, community(api.PluginSettings{Name: name, Source: g.repos[name], Enabled: on}))
	if resp.StatusCode != http.StatusOK {
		g.t.Fatalf("set %s enabled=%v: status %d: %s", name, on, resp.StatusCode, respText(g.t, resp))
	}
}

func (g *gateServer) task(title string) int {
	g.t.Helper()
	projects := g.s.settings.Projects(false)
	tk, err := g.s.board.Create(context.Background(), board.User, api.CreateTicket{Type: api.TypeTask, Title: title, Projects: []string{projects[0].ID}})
	if err != nil {
		g.t.Fatal(err)
	}
	return tk.Number
}

func (g *gateServer) ready(n int) {
	g.t.Helper()
	if _, err := g.s.board.Move(context.Background(), board.User, int64(n), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		g.t.Fatal(err)
	}
}

func (g *gateServer) get(n int) api.Ticket {
	g.t.Helper()
	tk, err := g.s.board.Get(context.Background(), board.User, int64(n))
	if err != nil {
		g.t.Fatal(err)
	}
	return tk
}

// wait polls until cond holds for ticket n and returns it.
func (g *gateServer) wait(n int, what string, cond func(api.Ticket) bool) api.Ticket {
	g.t.Helper()
	var tk api.Ticket
	waitFor(g.t, what, func() bool {
		tk = g.get(n)
		return cond(tk)
	})
	return tk
}

func (g *gateServer) action(plugin, action string, n int) {
	g.t.Helper()
	resp := asUser(g.b, g.s, "POST", "/api/plugins/"+plugin+"/actions/"+action, api.PluginRun{Ticket: n})
	if resp.StatusCode != http.StatusOK {
		g.t.Fatalf("action %s/%s: status %d: %s", plugin, action, resp.StatusCode, respText(g.t, resp))
	}
}

func (g *gateServer) thread(n int) string {
	g.t.Helper()
	list, err := g.s.board.Comments(context.Background(), board.User, int64(n))
	if err != nil {
		g.t.Fatal(err)
	}
	var out []string
	for _, c := range list {
		out = append(out, c.Body)
	}
	return strings.Join(out, "\n")
}

func gateOf(tk api.Ticket, plugin string) *api.TicketGate {
	if tk.Gate == nil {
		return nil
	}
	for i := range tk.Gate.Gates {
		if tk.Gate.Gates[i].Plugin == plugin {
			return &tk.Gate.Gates[i]
		}
	}
	return nil
}

func held(plugin string) func(api.Ticket) bool {
	return func(tk api.Ticket) bool {
		g := gateOf(tk, plugin)
		return g != nil && g.State == api.GateHeld
	}
}

func inColumn(col api.Column) func(api.Ticket) bool {
	return func(tk api.Ticket) bool { return tk.Column == col && tk.Gate == nil }
}

func TestGateReleaseGoesAheadWithoutAQuestion(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, nil)
	n := g.task("plain")
	g.ready(n)
	g.wait(n, "the ticket in Ready For Dev", inColumn(api.ColumnReady))
}

func TestGateReleaseHoldIsReleasedFromTheChoicesHook(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, nil)
	n := g.task("hold it")
	g.ready(n)
	tk := g.wait(n, "the hold", held("gate"))
	gate := gateOf(tk, "gate")
	wantChoices := []api.GateChoice{{Label: "Go on", Action: "go-on"}, {Label: "Stop", Action: "stop"}}
	if tk.Column != api.ColumnPlanning || gate.Reason != "waiting for you" || !slices.Equal(gate.Choices, wantChoices) {
		t.Fatalf("held ticket: column %s, gate %+v", tk.Column, gate)
	}
	g.action("gate", "go-on", n)
	g.wait(n, "the released ticket in Ready For Dev", inColumn(api.ColumnReady))
}

func TestGateReleaseHoldIsCancelledFromAHook(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, nil)
	n := g.task("hold it")
	g.ready(n)
	g.wait(n, "the hold", held("gate"))
	g.action("gate", "stop", n)
	g.wait(n, "the cancelled move", inColumn(api.ColumnPlanning))
}

func TestGateRefuseLeavesTheTicketWithTheReason(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, nil)
	n := g.task("refuse it")
	g.ready(n)
	g.wait(n, "the refusal", func(tk api.Ticket) bool { return tk.Gate == nil && strings.Contains(g.thread(n), "not now") })
	if tk := g.get(n); tk.Column != api.ColumnPlanning {
		t.Fatalf("a refused release left the ticket in %s", tk.Column)
	}
}

func TestGateFailuresGoAheadWithANoteAndALogLine(t *testing.T) {
	for _, title := range []string{"crash it", "garbage it"} {
		t.Run(title, func(t *testing.T) {
			g := newGateServer(t, false)
			g.install("gate", []string{"release"}, nil)
			n := g.task(title)
			g.ready(n)
			g.wait(n, "the move after the failure", inColumn(api.ColumnReady))
			if thread := g.thread(n); !strings.Contains(thread, "gate failed") || !strings.Contains(thread, "goes ahead") {
				t.Errorf("no note on the ticket: %q", thread)
			}
			var log []string
			for _, l := range g.s.plugins.m.LogLines("gate", 100) {
				log = append(log, l.Message)
			}
			if !strings.Contains(strings.Join(log, "\n"), "the move goes ahead") {
				t.Errorf("the plugin's log does not say the move went ahead: %v", log)
			}
		})
	}
}

func TestGateTimeoutGoesAheadWithANote(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, map[string]any{"timeout": "1s"})
	n := g.task("spin forever")
	g.ready(n)
	g.wait(n, "the move after the time limit", inColumn(api.ColumnReady))
	if thread := g.thread(n); !strings.Contains(thread, "time limit") {
		t.Errorf("no note about the time limit: %q", thread)
	}
}

func TestGateFailureThatHoldsFollowsTheManifest(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, map[string]any{"failure": "hold"})
	n := g.task("crash it")
	g.ready(n)
	tk := g.wait(n, "the hold by the failure", held("gate"))
	if reason := gateOf(tk, "gate").Reason; !strings.Contains(reason, "failed") || tk.Column != api.ColumnPlanning {
		t.Fatalf("held by a failure: column %s, reason %q", tk.Column, reason)
	}
}

func TestGateDisabledPluginCountsAsGoAhead(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, nil)
	n := g.task("hold it")
	g.ready(n)
	g.wait(n, "the hold", held("gate"))
	g.setEnabled("gate", false)
	g.wait(n, "the move after the plugin was disabled", inColumn(api.ColumnReady))
	if thread := g.thread(n); !strings.Contains(thread, "can no longer answer") {
		t.Errorf("no note on the ticket: %q", thread)
	}
}

func TestGateRemovedPluginCountsAsGoAhead(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, nil)
	n := g.task("hold it")
	g.ready(n)
	g.wait(n, "the hold", held("gate"))
	resp := asUser(g.b, g.s, "DELETE", "/api/plugins/gate", nil)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove: status %d: %s", resp.StatusCode, respText(t, resp))
	}
	g.wait(n, "the move after the plugin was removed", inColumn(api.ColumnReady))
}

func TestGateUserContinuesAnywayAndCancels(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, nil)
	a, c := g.task("hold a"), g.task("hold c")
	g.ready(a)
	g.ready(c)
	g.wait(a, "hold a", held("gate"))
	g.wait(c, "hold c", held("gate"))
	if resp := asUser(g.b, g.s, "POST", "/api/tickets/"+strconv.Itoa(a)+"/gate/override", struct{}{}); resp.StatusCode != http.StatusOK {
		t.Fatalf("override: status %d: %s", resp.StatusCode, respText(t, resp))
	}
	g.wait(a, "the overridden ticket in Ready For Dev", inColumn(api.ColumnReady))
	if resp := asUser(g.b, g.s, "POST", "/api/tickets/"+strconv.Itoa(c)+"/gate/cancel", api.GateCancelRequest{}); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: status %d: %s", resp.StatusCode, respText(t, resp))
	}
	g.wait(c, "the cancelled move", inColumn(api.ColumnPlanning))
}

func TestGateTwoPluginsBothHaveToAgree(t *testing.T) {
	g := newGateServer(t, false)
	g.install("alpha", []string{"release"}, nil)
	g.install("beta", []string{"release"}, nil)
	n := g.task("hold it")
	g.ready(n)
	tk := g.wait(n, "both holds", func(tk api.Ticket) bool { return held("alpha")(tk) && held("beta")(tk) })
	if gateOf(tk, "alpha").Reason != "waiting for you" || tk.Column != api.ColumnPlanning {
		t.Fatalf("gates %+v", tk.Gate)
	}
	g.action("alpha", "go-on", n)
	tk = g.wait(n, "alpha passed", func(tk api.Ticket) bool {
		a := gateOf(tk, "alpha")
		return a != nil && a.State == api.GatePassed
	})
	if tk.Column != api.ColumnPlanning || !held("beta")(tk) {
		t.Fatalf("one go-ahead moved the ticket or released beta: column %s, gates %+v", tk.Column, tk.Gate)
	}
	g.action("beta", "go-on", n)
	g.wait(n, "the move once both agreed", inColumn(api.ColumnReady))
}

// startedAgents is the number of agents the runner started.
func (g *gateServer) startedAgents() int { return len(g.runner.all()) }

func TestGateStartHoldsTheEngineUntilReleased(t *testing.T) {
	g := newGateServer(t, true)
	g.install("gate", []string{"start"}, nil)
	n := g.task("hold it")
	g.ready(n)
	tk := g.wait(n, "the hold of the start", held("gate"))
	if tk.Column != api.ColumnReady || tk.StartBlocked != api.StartBlockedGate || tk.Gate.Move != api.GateMoveStart {
		t.Fatalf("held start: column %s, start_blocked %q, gate %+v", tk.Column, tk.StartBlocked, tk.Gate)
	}
	// A ticket that has no gate of its own passes meanwhile: the engine
	// works, it just does not start the held ticket.
	other := g.task("plain")
	g.ready(other)
	g.wait(other, "the plain ticket started", func(tk api.Ticket) bool { return tk.Column == api.ColumnInProgress })
	if tk := g.get(n); tk.Column != api.ColumnReady {
		t.Fatalf("the held ticket moved to %s", tk.Column)
	}
	g.action("gate", "go-on", n)
	g.wait(n, "the released ticket started", func(tk api.Ticket) bool { return tk.Column == api.ColumnInProgress })
}

func TestGateStartCancelPutsTheTicketBackIntoPlanning(t *testing.T) {
	g := newGateServer(t, true)
	g.install("gate", []string{"start"}, nil)
	n := g.task("hold it")
	g.ready(n)
	g.wait(n, "the hold of the start", held("gate"))
	g.action("gate", "stop", n)
	g.wait(n, "the ticket back in Planning", inColumn(api.ColumnPlanning))
	if g.startedAgents() != 0 {
		t.Fatalf("%d agents started for a cancelled start", g.startedAgents())
	}
}

// reopen closes the server and opens another on the same home, the way a
// restart of omo does.
func (g *gateServer) reopen() {
	g.t.Helper()
	layout, cfg := g.s.opts.Layout, g.s.opts.Config
	g.s.Close()
	g.s = openServer(g.t, layout, cfg)
	g.b = newBrowser(g.t, g.s)
}

func TestGateHoldSurvivesARestart(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, nil)
	n := g.task("hold it")
	g.ready(n)
	g.wait(n, "the hold", held("gate"))

	g.reopen()
	tk := g.get(n)
	gate := gateOf(tk, "gate")
	if tk.Column != api.ColumnPlanning || gate == nil || gate.State != api.GateHeld || gate.Reason != "waiting for you" || len(gate.Choices) != 2 {
		t.Fatalf("after the restart: column %s, gate %+v", tk.Column, tk.Gate)
	}
	// The plugin is loaded again and can still let the move go.
	g.action("gate", "go-on", n)
	g.wait(n, "the released ticket in Ready For Dev", inColumn(api.ColumnReady))
}

func TestGatePendingGateIsAskedAgainAfterARestart(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, nil)
	n := g.task("hold it")
	// What a stop of omo during the gate hook leaves behind: the request
	// and a gate that never answered.
	for _, q := range []string{
		`INSERT INTO ticket_gate_moves (ticket_id, move, actor_role, created_at) VALUES (` + strconv.Itoa(n) + `, 'release', 'user', '2026-11-19T10:00:00.000000000Z')`,
		`INSERT INTO ticket_gates (ticket_id, plugin, state, updated_at) VALUES (` + strconv.Itoa(n) + `, 'gate', 'pending', '2026-11-19T10:00:00.000000000Z')`,
	} {
		if _, err := g.s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	g.reopen()
	g.wait(n, "the hold after the restart", held("gate"))
}

func TestGateUnavailablePluginKeepsItsFailureHold(t *testing.T) {
	for _, how := range []string{"disabled", "removed"} {
		t.Run(how, func(t *testing.T) {
			g := newGateServer(t, false)
			g.install("gate", []string{"release"}, map[string]any{"failure": "hold"})
			n := g.task("hold it")
			g.ready(n)
			g.wait(n, "the hold", held("gate"))
			if how == "disabled" {
				g.setEnabled("gate", false)
			} else if resp := asUser(g.b, g.s, "DELETE", "/api/plugins/gate", nil); resp.StatusCode >= 300 {
				t.Fatalf("remove: status %d", resp.StatusCode)
			}
			// Wait for the plugin change to have been seen by the runner.
			waitFor(t, "the plugin to be gone", func() bool { return len(g.s.plugins.m.GatePlugins(api.GateMoveRelease)) == 0 })
			g.s.gates.poke()
			g.s.askOpenGates(context.Background())
			if tk := g.get(n); tk.Column != api.ColumnPlanning || !held("gate")(tk) {
				t.Fatalf("a failure that holds let the ticket go: column %s, gate %+v", tk.Column, tk.Gate)
			}
			if err := g.s.board.GateOverride(context.Background(), board.User, int64(n)); err != nil {
				t.Fatal(err)
			}
			g.wait(n, "the override", inColumn(api.ColumnReady))
		})
	}
}

func TestGateRemovedPluginLeavesALogLine(t *testing.T) {
	g := newGateServer(t, false)
	g.install("gate", []string{"release"}, nil)
	n := g.task("hold it")
	g.ready(n)
	g.wait(n, "the hold", held("gate"))
	if resp := asUser(g.b, g.s, "DELETE", "/api/plugins/gate", nil); resp.StatusCode >= 300 {
		t.Fatalf("remove: status %d", resp.StatusCode)
	}
	g.wait(n, "the move", inColumn(api.ColumnReady))
	var log []string
	for _, l := range g.s.plugins.m.LogLines("gate", 100) {
		log = append(log, l.Message)
	}
	if !strings.Contains(strings.Join(log, "\n"), "the move goes ahead") {
		t.Fatalf("no log line of the removed plugin: %v", log)
	}
}
