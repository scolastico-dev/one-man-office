package board

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// fakeGates is the board's view of the plugins that gate a move.
type fakeGates struct {
	mu      sync.Mutex
	plugins map[api.GateMove][]string
}

func (g *fakeGates) GatePlugins(move api.GateMove) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.plugins[move])
}

func gatedBoard(t *testing.T, move api.GateMove, plugins ...string) *Board {
	t.Helper()
	b, _ := newTestBoard(t)
	b.SetGates(&fakeGates{plugins: map[api.GateMove][]string{move: plugins}})
	return b
}

func newTask(t *testing.T, b *Board) int {
	t.Helper()
	return mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "task", Projects: []string{"api"}}).Number
}

func moveTo(t *testing.T, b *Board, n int, col api.Column) api.Ticket {
	t.Helper()
	tk, err := b.Move(context.Background(), User, int64(n), api.MoveRequest{Column: col})
	if err != nil {
		t.Fatalf("move #%d to %s: %v", n, col, err)
	}
	return tk
}

func gateKinds(t *testing.T, b *Board, last int64) []string {
	t.Helper()
	var out []string
	for _, e := range eventsAfter(t, b, last) {
		switch e.Kind {
		case api.KindTicketHeld, api.KindTicketReleased, api.KindTicketRefused, api.KindTicketOverridden, api.KindTicketHoldCanceled:
			out = append(out, e.Kind)
		}
	}
	return out
}

func answer(t *testing.T, b *Board, n int, plugin string, a GateAnswer) {
	t.Helper()
	if err := b.GateAnswer(context.Background(), int64(n), plugin, a); err != nil {
		t.Fatalf("answer of %s on #%d: %v", plugin, n, err)
	}
}

func TestGateHoldsTheReleaseUntilThePluginGoesAhead(t *testing.T) {
	b := gatedBoard(t, api.GateMoveRelease, "pull")
	n := newTask(t, b)
	last := lastEvent(t, b)

	tk := moveTo(t, b, n, api.ColumnReady)
	if tk.Column != api.ColumnPlanning {
		t.Fatalf("a gated release moved the ticket to %s before the plugin answered", tk.Column)
	}
	if tk.Gate == nil || tk.Gate.Move != api.GateMoveRelease || tk.Gate.From != api.ColumnPlanning || tk.Gate.To != api.ColumnReady ||
		len(tk.Gate.Gates) != 1 || tk.Gate.Gates[0].Plugin != "pull" || tk.Gate.Gates[0].State != api.GatePending {
		t.Fatalf("gate = %+v, want the release pending for pull", tk.Gate)
	}

	answer(t, b, n, "pull", GateAnswer{Kind: GateGoAhead})
	tk = get(t, b, n)
	if tk.Column != api.ColumnReady || tk.Gate != nil {
		t.Fatalf("after the go-ahead: column %s, gate %+v; want Ready For Dev and no gate", tk.Column, tk.Gate)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 1 || moved[0].From != api.ColumnPlanning || moved[0].To != api.ColumnReady || moved[0].Action != ActionApprove {
		t.Fatalf("moved events = %+v, want one approve Planning -> Ready", moved)
	}
	if got := gateKinds(t, b, last); !slices.Equal(got, []string{api.KindTicketReleased}) {
		t.Fatalf("gate events = %v, want [ticket.released]", got)
	}
}

func TestGateWithoutGatingPluginsMovesAtOnce(t *testing.T) {
	b := gatedBoard(t, api.GateMoveStart, "pull")
	n := newTask(t, b)
	if tk := moveTo(t, b, n, api.ColumnReady); tk.Column != api.ColumnReady || tk.Gate != nil {
		t.Fatalf("a release nobody gates ended in %s with gate %+v", tk.Column, tk.Gate)
	}
}

func TestGateHoldRecordsReasonAndChoicesAndSurvivesUntilReleased(t *testing.T) {
	b := gatedBoard(t, api.GateMoveRelease, "pull")
	n := newTask(t, b)
	moveTo(t, b, n, api.ColumnReady)
	last := lastEvent(t, b)

	choices := []api.GateChoice{{Label: "Pull", Action: "pull"}, {Label: "Skip", Action: "skip"}}
	answer(t, b, n, "pull", GateAnswer{Kind: GateHold, Reason: "main is 3 commits behind", Choices: choices})
	tk := get(t, b, n)
	g := tk.Gate.Gates[0]
	if tk.Column != api.ColumnPlanning || g.State != api.GateHeld || g.Reason != "main is 3 commits behind" || !slices.Equal(g.Choices, choices) {
		t.Fatalf("held ticket: column %s, gate %+v", tk.Column, g)
	}
	if got := gateKinds(t, b, last); !slices.Equal(got, []string{api.KindTicketHeld}) {
		t.Fatalf("gate events = %v, want [ticket.held]", got)
	}

	// A held plugin gives its go-ahead later, from one of its hooks.
	if err := b.GateRelease(context.Background(), int64(n), "pull"); err != nil {
		t.Fatal(err)
	}
	if tk := get(t, b, n); tk.Column != api.ColumnReady || tk.Gate != nil {
		t.Fatalf("after the release: column %s, gate %+v", tk.Column, tk.Gate)
	}
}

func TestGateRefuseDoesNotMoveAndSaysWhy(t *testing.T) {
	b := gatedBoard(t, api.GateMoveRelease, "policy")
	n := newTask(t, b)
	moveTo(t, b, n, api.ColumnReady)
	last := lastEvent(t, b)

	answer(t, b, n, "policy", GateAnswer{Kind: GateRefuse, Reason: "no release on fridays"})
	tk := get(t, b, n)
	if tk.Column != api.ColumnPlanning || tk.Gate != nil {
		t.Fatalf("refused ticket: column %s, gate %+v", tk.Column, tk.Gate)
	}
	if got := gateKinds(t, b, last); !slices.Equal(got, []string{api.KindTicketRefused}) {
		t.Fatalf("gate events = %v, want [ticket.refused]", got)
	}
	thread := mustComments(t, b, n)
	if !strings.Contains(thread, "policy") || !strings.Contains(thread, "no release on fridays") {
		t.Fatalf("the refusal is not on the ticket: %q", thread)
	}
}

func mustComments(t *testing.T, b *Board, n int) string {
	t.Helper()
	list, err := b.Comments(context.Background(), User, int64(n))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range list {
		out = append(out, c.Body)
	}
	return strings.Join(out, "\n")
}

func TestGateTwoPluginsBothHaveToAgree(t *testing.T) {
	b := gatedBoard(t, api.GateMoveRelease, "alpha", "beta")
	n := newTask(t, b)
	moveTo(t, b, n, api.ColumnReady)

	answer(t, b, n, "alpha", GateAnswer{Kind: GateGoAhead})
	tk := get(t, b, n)
	if tk.Column != api.ColumnPlanning || len(tk.Gate.Gates) != 2 {
		t.Fatalf("one go-ahead moved the ticket: column %s, gate %+v", tk.Column, tk.Gate)
	}
	if tk.Gate.Gates[0].Plugin != "alpha" || tk.Gate.Gates[0].State != api.GatePassed || tk.Gate.Gates[1].Plugin != "beta" || tk.Gate.Gates[1].State != api.GatePending {
		t.Fatalf("gates = %+v", tk.Gate.Gates)
	}
	answer(t, b, n, "beta", GateAnswer{Kind: GateHold, Reason: "wait"})
	answer(t, b, n, "beta", GateAnswer{Kind: GateGoAhead})
	if tk := get(t, b, n); tk.Column != api.ColumnReady {
		t.Fatalf("both gave their go-ahead, the ticket is in %s", tk.Column)
	}
}

func TestGateFailureNoteGoesOnTheTicket(t *testing.T) {
	b := gatedBoard(t, api.GateMoveRelease, "pull")
	n := newTask(t, b)
	moveTo(t, b, n, api.ColumnReady)
	answer(t, b, n, "pull", GateAnswer{Kind: GateGoAhead, Note: "the gate hook of pull failed (time limit reached); the move goes ahead"})
	if tk := get(t, b, n); tk.Column != api.ColumnReady {
		t.Fatalf("column %s", tk.Column)
	}
	if thread := mustComments(t, b, n); !strings.Contains(thread, "time limit reached") {
		t.Fatalf("no note on the ticket: %q", thread)
	}
}

func TestGateUserOverridesEveryHold(t *testing.T) {
	b := gatedBoard(t, api.GateMoveRelease, "alpha", "beta")
	n := newTask(t, b)
	moveTo(t, b, n, api.ColumnReady)
	answer(t, b, n, "alpha", GateAnswer{Kind: GateHold, Reason: "no"})
	last := lastEvent(t, b)

	if err := b.GateOverride(context.Background(), User, int64(n)); err != nil {
		t.Fatal(err)
	}
	if tk := get(t, b, n); tk.Column != api.ColumnReady || tk.Gate != nil {
		t.Fatalf("after the override: column %s, gate %+v", tk.Column, tk.Gate)
	}
	if got := gateKinds(t, b, last); !slices.Equal(got, []string{api.KindTicketOverridden}) {
		t.Fatalf("gate events = %v, want [ticket.overridden]", got)
	}
	agentActor := Actor{Role: api.RoleCEO}
	n2 := newTask(t, b)
	moveTo(t, b, n2, api.ColumnReady)
	wantCode(t, b.GateOverride(context.Background(), agentActor, int64(n2)), api.CodeForbidden)
}

func TestGateCancelOfTheReleaseLeavesTheTicketInPlanning(t *testing.T) {
	b := gatedBoard(t, api.GateMoveRelease, "pull")
	n := newTask(t, b)
	moveTo(t, b, n, api.ColumnReady)
	answer(t, b, n, "pull", GateAnswer{Kind: GateHold, Reason: "behind"})
	last := lastEvent(t, b)

	if err := b.GateCancel(context.Background(), User, int64(n), ""); err != nil {
		t.Fatal(err)
	}
	if tk := get(t, b, n); tk.Column != api.ColumnPlanning || tk.Gate != nil {
		t.Fatalf("after the cancel: column %s, gate %+v", tk.Column, tk.Gate)
	}
	if got := gateKinds(t, b, last); !slices.Equal(got, []string{api.KindTicketHoldCanceled}) {
		t.Fatalf("gate events = %v", got)
	}
}

// startGated puts a ticket in Ready For Dev and asks the start gates.
func startGated(t *testing.T, b *Board, plugins ...string) int {
	t.Helper()
	n := newTask(t, b)
	moveTo(t, b, n, api.ColumnReady)
	gated, err := b.RequestStartGates(context.Background(), int64(n))
	if err != nil || !gated {
		t.Fatalf("RequestStartGates = %v, %v; want a request for %v", gated, err, plugins)
	}
	return n
}

func TestGateStartIsHeldAndShownAsStartBlocked(t *testing.T) {
	b := gatedBoard(t, api.GateMoveStart, "pull")
	n := startGated(t, b, "pull")
	tk := get(t, b, n)
	if tk.Column != api.ColumnReady || tk.StartBlocked != api.StartBlockedGate || tk.Gate == nil || tk.Gate.Move != api.GateMoveStart || tk.Gate.To != api.ColumnInProgress {
		t.Fatalf("pending start: column %s, start_blocked %q, gate %+v", tk.Column, tk.StartBlocked, tk.Gate)
	}
	answer(t, b, n, "pull", GateAnswer{Kind: GateGoAhead})
	tk = get(t, b, n)
	if tk.Column != api.ColumnReady || tk.StartBlocked != "" || tk.Gate == nil || tk.Gate.Gates[0].State != api.GatePassed {
		t.Fatalf("passed start: column %s, start_blocked %q, gate %+v", tk.Column, tk.StartBlocked, tk.Gate)
	}
	// The engine's start carries the move out and clears the gate.
	if err := b.BeginWork(context.Background(), int64(n)); err != nil {
		t.Fatal(err)
	}
	if tk := get(t, b, n); tk.Column != api.ColumnInProgress || tk.Gate != nil {
		t.Fatalf("started ticket: column %s, gate %+v", tk.Column, tk.Gate)
	}
}

func TestGateCancelOfTheStartPutsTheTicketBackIntoPlanning(t *testing.T) {
	b := gatedBoard(t, api.GateMoveStart, "pull")
	n := startGated(t, b, "pull")
	answer(t, b, n, "pull", GateAnswer{Kind: GateHold, Reason: "behind"})
	if err := b.GateCancel(context.Background(), User, int64(n), ""); err != nil {
		t.Fatal(err)
	}
	if tk := get(t, b, n); tk.Column != api.ColumnPlanning || tk.Gate != nil || tk.StartBlocked != "" {
		t.Fatalf("after the cancel: column %s, gate %+v", tk.Column, tk.Gate)
	}
}

func TestGateRefuseOfTheStartPutsTheTicketBackIntoPlanning(t *testing.T) {
	b := gatedBoard(t, api.GateMoveStart, "pull")
	n := startGated(t, b, "pull")
	answer(t, b, n, "pull", GateAnswer{Kind: GateRefuse, Reason: "not now"})
	if tk := get(t, b, n); tk.Column != api.ColumnPlanning || tk.Gate != nil {
		t.Fatalf("after the refusal: column %s, gate %+v", tk.Column, tk.Gate)
	}
}

func TestGateOverrideOfTheStartLetsTheEngineGo(t *testing.T) {
	b := gatedBoard(t, api.GateMoveStart, "pull")
	n := startGated(t, b, "pull")
	answer(t, b, n, "pull", GateAnswer{Kind: GateHold, Reason: "behind"})
	if err := b.GateOverride(context.Background(), User, int64(n)); err != nil {
		t.Fatal(err)
	}
	tk := get(t, b, n)
	if tk.Column != api.ColumnReady || tk.Gate == nil || tk.Gate.Gates[0].State != api.GatePassed || tk.StartBlocked != "" {
		t.Fatalf("overridden start: column %s, gate %+v", tk.Column, tk.Gate)
	}
}

func TestGateAnswerForAMoveThatIsGoneIsRefused(t *testing.T) {
	b := gatedBoard(t, api.GateMoveRelease, "pull")
	n := newTask(t, b)
	err := b.GateAnswer(context.Background(), int64(n), "pull", GateAnswer{Kind: GateGoAhead})
	wantCode(t, err, api.CodeConflict)
}

func TestGatePendingListsWhatNeedsAnAnswer(t *testing.T) {
	b := gatedBoard(t, api.GateMoveRelease, "alpha", "beta")
	n := newTask(t, b)
	moveTo(t, b, n, api.ColumnReady)
	answer(t, b, n, "alpha", GateAnswer{Kind: GateHold, Reason: "wait"})
	got, err := b.OpenGates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []OpenGate{{Ticket: int64(n), Move: api.GateMoveRelease, Plugin: "alpha", State: api.GateHeld}, {Ticket: int64(n), Move: api.GateMoveRelease, Plugin: "beta", State: api.GatePending}}
	if !slices.Equal(got, want) {
		raw, _ := json.Marshal(got)
		t.Fatalf("open gates = %s", raw)
	}
}
