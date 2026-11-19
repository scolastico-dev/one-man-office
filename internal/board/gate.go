package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// Plugin gates. A plugin can gate a ticket's move from Planning to Ready For
// Dev (api.GateMoveRelease) and from Ready For Dev to In Progress
// (api.GateMoveStart). The board knows only which plugins gate a move
// (Gates) and what each of them answered; the plugin runtime runs the hooks
// and reports their answers through GateAnswer, never inside a board
// transaction.
//
// A request is one row per ticket (ticket_gate_moves: the move, who asked
// for it, where it goes) with one row per gating plugin (ticket_gates:
// pending, held or passed). The ticket stays in its column until every
// plugin passed. For the release the board then carries the move out as the
// actor who asked; for the start it leaves the passed rows for the engine,
// whose BeginWork moves the ticket (every column change clears the rows).
// Everything is stored, so a hold survives a restart; a pending gate is
// asked again by whoever reads OpenGates.

// Gates tells the board which plugins gate a move. It must not use the
// board's database.
type Gates interface {
	GatePlugins(move api.GateMove) []string
	// GateFailureHolds reports whether plugin's gate for move holds when it
	// fails (or its plugin goes away) instead of going ahead.
	GateFailureHolds(plugin string, move api.GateMove) bool
}

// GateKind is how a plugin's gate hook answered.
type GateKind string

const (
	// GateGoAhead: the plugin lets the move happen.
	GateGoAhead GateKind = "go_ahead"
	// GateHold: the plugin keeps the ticket where it is until it gives its
	// go-ahead from a later hook.
	GateHold GateKind = "hold"
	// GateRefuse: the move is not made.
	GateRefuse GateKind = "refuse"
)

// GateAnswer is a plugin's answer to a gate. Reason and Choices belong to a
// hold or a refusal; Note is an optional line for the ticket's thread (what
// went wrong when a failing gate let the move go ahead).
type GateAnswer struct {
	Kind    GateKind
	Reason  string
	Choices []api.GateChoice
	Note    string
}

// OpenGate is one gate of a ticket that has not passed: the plugin's hook
// is asked (pending) or the plugin holds the move (held).
type OpenGate struct {
	// Request identifies the request the gate belongs to; an answer for
	// an earlier request of the ticket is refused.
	Request int64
	// FailureHold is the plugin's failure policy when the request was made.
	FailureHold bool
	Ticket      int64
	Move        api.GateMove
	Plugin      string
	State       api.GateState
}

// Bounds of what a plugin says about a hold.
const (
	maxGateReason  = 1000
	maxGateChoices = 8
	maxGateLabel   = 200
	maxGateNote    = 2000
)

// Actions recorded in ticket.moved events of gated moves.
const (
	// ActionGateCancel: the held start was cancelled and the ticket went
	// back to Planning.
	ActionGateCancel = "gate_cancel"
	// ActionGateRefuse: a plugin refused the start and the ticket went
	// back to Planning.
	ActionGateRefuse = "gate_refuse"
)

var gateActionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// SetGates tells the board which plugins gate which move; nil means none.
func (b *Board) SetGates(g Gates) {
	b.gatesMu.Lock()
	b.gates = g
	b.gatesMu.Unlock()
}

func (b *Board) gateFailureHolds(plugin string, move api.GateMove) bool {
	b.gatesMu.RLock()
	g := b.gates
	b.gatesMu.RUnlock()
	return g != nil && g.GateFailureHolds(plugin, move)
}

// gatePlugins are the plugins that gate move, sorted and without repeats.
func (b *Board) gatePlugins(move api.GateMove) []string {
	b.gatesMu.RLock()
	g := b.gates
	b.gatesMu.RUnlock()
	if g == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, name := range g.GatePlugins(move) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// gateMoveRow is the stored request of a ticket's gated move.
type gateMoveRow struct {
	id        int64
	move      api.GateMove
	actorRole api.Role
	actorName string
	before    *int
}

func loadGateMove(q db.Queryer, n int64) (gateMoveRow, bool, error) {
	var r gateMoveRow
	var before sql.NullInt64
	err := q.QueryRow(`SELECT id, move, actor_role, actor_name, before_ticket FROM ticket_gate_moves WHERE ticket_id = ?`, n).
		Scan(&r.id, &r.move, &r.actorRole, &r.actorName, &before)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	if before.Valid {
		v := int(before.Int64)
		r.before = &v
	}
	return r, true, nil
}

func gateColumns(move api.GateMove) (from, to api.Column) {
	if move == api.GateMoveStart {
		return api.ColumnReady, api.ColumnInProgress
	}
	return api.ColumnPlanning, api.ColumnReady
}

// loadGateRows are the plugin rows of ticket n in the order they were made.
func loadGateRows(q db.Queryer, n int64) ([]api.TicketGate, error) {
	out := []api.TicketGate{}
	err := each(q, `SELECT plugin, state, reason, choices FROM ticket_gates WHERE ticket_id = ? ORDER BY rowid`, n, func(rows *sql.Rows) error {
		var g api.TicketGate
		var raw string
		if err := rows.Scan(&g.Plugin, &g.State, &g.Reason, &raw); err != nil {
			return err
		}
		g.Choices = []api.GateChoice{}
		if err := json.Unmarshal([]byte(raw), &g.Choices); err != nil || g.Choices == nil {
			g.Choices = []api.GateChoice{}
		}
		out = append(out, g)
		return nil
	})
	return out, err
}

// attachGates fills Gate (and the derived start-blocked reason) of the
// summaries in byID, from two queries.
func attachGates(q db.Queryer, byID map[int64]*api.TicketSummary, idJSON string) error {
	if err := each(q, `SELECT ticket_id, move FROM ticket_gate_moves WHERE ticket_id`+idsIn, idJSON, func(rows *sql.Rows) error {
		var id int64
		var move api.GateMove
		if err := rows.Scan(&id, &move); err != nil {
			return err
		}
		from, to := gateColumns(move)
		byID[id].Gate = &api.TicketGates{Move: move, From: from, To: to, Gates: []api.TicketGate{}}
		return nil
	}); err != nil {
		return err
	}
	return each(q, `SELECT ticket_id, plugin, state, reason, choices FROM ticket_gates WHERE ticket_id`+idsIn+` ORDER BY ticket_id, rowid`,
		idJSON, func(rows *sql.Rows) error {
			var id int64
			var g api.TicketGate
			var raw string
			if err := rows.Scan(&id, &g.Plugin, &g.State, &g.Reason, &raw); err != nil {
				return err
			}
			g.Choices = []api.GateChoice{}
			if err := json.Unmarshal([]byte(raw), &g.Choices); err != nil || g.Choices == nil {
				g.Choices = []api.GateChoice{}
			}
			s := byID[id]
			if s.Gate == nil {
				return nil
			}
			s.Gate.Gates = append(s.Gate.Gates, g)
			if s.Gate.Move == api.GateMoveStart && g.State != api.GatePassed && s.StartBlocked == api.StartBlockedNone {
				s.StartBlocked = api.StartBlockedGate
			}
			return nil
		})
}

// requestGates records the request for move on t and one pending row per
// plugin, and reports the change.
func (b *Board) requestGates(tx *sql.Tx, a Actor, t ticketRow, move api.GateMove, plugins []string, before *int) error {
	now := b.stamp()
	var bt any
	if before != nil {
		bt = int64(*before)
	}
	if _, err := tx.Exec(`INSERT INTO ticket_gate_moves (ticket_id, move, actor_role, actor_name, before_ticket, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, t.ID, move, a.Role, a.Name, bt, now); err != nil {
		return err
	}
	for _, p := range plugins {
		if _, err := tx.Exec(`INSERT INTO ticket_gates (ticket_id, plugin, failure_hold, state, updated_at) VALUES (?, ?, ?, 'pending', ?)`,
			t.ID, p, b.gateFailureHolds(p, move), now); err != nil {
			return err
		}
	}
	return b.emitUpdated(tx, a, t.ID, "gate")
}

// gateRelease is the part of approve that asks the plugins that gate the
// release. It reports true when the move waits for them (the ticket stays in
// Planning).
func (b *Board) gateRelease(tx *sql.Tx, a Actor, t ticketRow, before *int) (bool, error) {
	if _, ok, err := loadGateMove(tx, t.ID); err != nil || ok {
		return true, err
	}
	plugins := b.gatePlugins(api.GateMoveRelease)
	if len(plugins) == 0 {
		return false, nil
	}
	return true, b.requestGates(tx, a, t, api.GateMoveRelease, plugins, before)
}

// RequestStartGates asks the plugins that gate the start of ticket n, which
// is in Ready For Dev, and reports whether the start waits for them. It
// reports false when no plugin gates the start (the engine goes on) and
// when the ticket has no longer anything to start. A ticket whose gates
// were already asked reports true until they all passed; once they did, the
// engine's start goes on (false).
func (b *Board) RequestStartGates(ctx context.Context, n int64) (bool, error) {
	var waits bool
	err := b.writeChanges(ctx, func(tx *sql.Tx) (bool, error) {
		t, err := loadTicket(tx, n)
		if err != nil {
			return false, err
		}
		if t.Col != api.ColumnReady {
			return false, nil
		}
		if _, ok, err := loadGateMove(tx, n); err != nil {
			return false, err
		} else if ok {
			rows, err := loadGateRows(tx, n)
			if err != nil {
				return false, err
			}
			for _, g := range rows {
				waits = waits || g.State != api.GatePassed
			}
			return false, nil
		}
		plugins := b.gatePlugins(api.GateMoveStart)
		if len(plugins) == 0 {
			return false, nil
		}
		waits = true
		return true, b.requestGates(tx, System, t, api.GateMoveStart, plugins, nil)
	})
	return waits, err
}

// OpenGates lists every gate that has not passed, by ticket and plugin.
func (b *Board) OpenGates(ctx context.Context) ([]OpenGate, error) {
	out := []OpenGate{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT m.id, g.failure_hold, g.ticket_id, m.move, g.plugin, g.state FROM ticket_gates g
			JOIN ticket_gate_moves m ON m.ticket_id = g.ticket_id WHERE g.state <> 'passed' ORDER BY g.ticket_id, g.rowid`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g OpenGate
			if err := rows.Scan(&g.Request, &g.FailureHold, &g.Ticket, &g.Move, &g.Plugin, &g.State); err != nil {
				return err
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	return out, err
}

// clearGates drops the gate rows of ticket n; every column change calls it.
func clearGates(tx *sql.Tx, n int64) error {
	if _, err := tx.Exec(`DELETE FROM ticket_gates WHERE ticket_id = ?`, n); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM ticket_gate_moves WHERE ticket_id = ?`, n)
	return err
}

func cutRunes(s string, n int) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "�")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func cleanChoices(in []api.GateChoice) ([]api.GateChoice, error) {
	if len(in) > maxGateChoices {
		return nil, api.Errorf(api.CodeValidation, "a hold offers at most %d choices", maxGateChoices)
	}
	out := make([]api.GateChoice, 0, len(in))
	for _, c := range in {
		label := cutRunes(c.Label, maxGateLabel)
		if label == "" || !gateActionPattern.MatchString(c.Action) {
			return nil, api.Errorf(api.CodeValidation, "a choice needs a label and the name of a manual hook")
		}
		out = append(out, api.GateChoice{Label: label, Action: c.Action})
	}
	return out, nil
}

// gateEvent emits one of the gate events for ticket n.
func (b *Board) gateEvent(tx *sql.Tx, a Actor, kind string, n int64, move api.GateMove, plugin, reason string, choices []api.GateChoice) error {
	s, err := b.summary(tx, n)
	if err != nil {
		return err
	}
	if choices == nil {
		choices = []api.GateChoice{}
	}
	return emit(tx, kind, n, a, api.TicketGateData{Ticket: s, Move: move, Plugin: plugin, Reason: reason, Choices: choices})
}

// openRequest loads the request of ticket n or answers conflict.
func openRequest(tx *sql.Tx, n int64) (gateMoveRow, []api.TicketGate, error) {
	m, ok, err := loadGateMove(tx, n)
	if err != nil {
		return m, nil, err
	}
	if !ok {
		return m, nil, api.Errorf(api.CodeConflict, "#%d has no move that waits for a plugin", n)
	}
	rows, err := loadGateRows(tx, n)
	return m, rows, err
}

func gateOf(rows []api.TicketGate, plugin string) *api.TicketGate {
	for i := range rows {
		if rows[i].Plugin == plugin {
			return &rows[i]
		}
	}
	return nil
}

// GateAnswer records plugin's answer to the gate of request (the
// OpenGate's Request) of ticket n (see
// GateAnswer). It answers conflict when the ticket has no such gate any more
// (the move was made, cancelled or overridden meanwhile), which the caller
// takes as nothing to do.
func (b *Board) GateAnswer(ctx context.Context, n, request int64, plugin string, ans GateAnswer) error {
	reason := cutRunes(ans.Reason, maxGateReason)
	note := cutRunes(ans.Note, maxGateNote)
	choices, err := cleanChoices(ans.Choices)
	if err != nil {
		return err
	}
	switch ans.Kind {
	case GateGoAhead, GateHold, GateRefuse:
	default:
		return api.Errorf(api.CodeValidation, "unknown gate answer %q", ans.Kind)
	}
	if ans.Kind != GateHold {
		choices = []api.GateChoice{}
	}
	if ans.Kind != GateGoAhead && reason == "" {
		reason = "no reason given"
	}
	return b.write(ctx, func(tx *sql.Tx) error {
		m, rows, err := openRequest(tx, n)
		if err != nil {
			return err
		}
		g := gateOf(rows, plugin)
		if m.id != request || g == nil || g.State == api.GatePassed {
			return api.Errorf(api.CodeConflict, "plugin %s has no open gate on #%d", plugin, n)
		}
		if note != "" {
			if err := b.systemComment(tx, n, note); err != nil {
				return err
			}
		}
		switch ans.Kind {
		case GateGoAhead:
			return b.pass(tx, System, n, m, plugin)
		case GateHold:
			raw, err := json.Marshal(choices)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE ticket_gates SET state = 'held', reason = ?, choices = ?, updated_at = ? WHERE ticket_id = ? AND plugin = ?`,
				reason, string(raw), b.stamp(), n, plugin); err != nil {
				return err
			}
			return b.gateEvent(tx, System, api.KindTicketHeld, n, m.move, plugin, reason, choices)
		default:
			return b.refuse(tx, n, m, plugin, reason)
		}
	})
}

// GateRelease is the go-ahead plugin gives later, from one of its hooks,
// to a move it holds (or has not answered yet).
func (b *Board) GateRelease(ctx context.Context, n int64, plugin string) error {
	return b.write(ctx, func(tx *sql.Tx) error {
		m, rows, err := openRequest(tx, n)
		if err != nil {
			return err
		}
		g := gateOf(rows, plugin)
		if g == nil {
			return api.Errorf(api.CodeConflict, "plugin %s does not gate the move of #%d", plugin, n)
		}
		if g.State == api.GatePassed {
			return nil
		}
		return b.pass(tx, System, n, m, plugin)
	})
}

// pass records plugin's go-ahead and, when it was the last one, lets the
// move go on.
func (b *Board) pass(tx *sql.Tx, a Actor, n int64, m gateMoveRow, plugin string) error {
	if _, err := tx.Exec(`UPDATE ticket_gates SET state = 'passed', reason = '', choices = '[]', updated_at = ? WHERE ticket_id = ? AND plugin = ?`,
		b.stamp(), n, plugin); err != nil {
		return err
	}
	return b.proceedIfPassed(tx, a, n, m, true)
}

// proceedIfPassed carries the move on when every plugin passed. For the
// release the ticket moves to Ready For Dev as the actor who asked for it;
// for the start the passed rows stay and the engine's start moves it.
func (b *Board) proceedIfPassed(tx *sql.Tx, a Actor, n int64, m gateMoveRow, announce bool) error {
	rows, err := loadGateRows(tx, n)
	if err != nil {
		return err
	}
	for _, g := range rows {
		if g.State != api.GatePassed {
			return b.emitUpdated(tx, a, n, "gate")
		}
	}
	if announce {
		if err := b.gateEvent(tx, a, api.KindTicketReleased, n, m.move, "", "", nil); err != nil {
			return err
		}
	}
	if m.move == api.GateMoveStart {
		return b.emitUpdated(tx, a, n, "gate")
	}
	t, err := loadTicket(tx, n)
	if err != nil {
		return err
	}
	if err := clearGates(tx, n); err != nil {
		return err
	}
	asker := Actor{Role: m.actorRole, Name: m.actorName}
	if err := b.completeRelease(tx, asker, t, m.before); err != nil {
		// The move cannot be made any more (a project went, the ticket is
		// busy). Say so on the ticket; nothing is left waiting.
		var ae *api.Error
		if errors.As(err, &ae) {
			return b.systemComment(tx, n, fmt.Sprintf("The release was cleared by every plugin but cannot be made: %s", ae.Message))
		}
		return err
	}
	return nil
}

// completeRelease moves t from Planning to Ready For Dev after its gates
// passed, with the checks of approve that depend on the state of the
// board; who may release was checked when it was asked for.
func (b *Board) completeRelease(tx *sql.Tx, asker Actor, t ticketRow, before *int) error {
	if t.Col != api.ColumnPlanning {
		return api.Errorf(api.CodeInvalidTransition, "#%d is in %s, not in Planning", t.ID, columnWords(t.Col))
	}
	if err := checkNotBusy(tx, t); err != nil {
		return err
	}
	ps, err := loadProjects(tx, t.ID)
	if err != nil {
		return err
	}
	for _, pr := range ps {
		if _, err := b.availableProject(pr.id); err != nil {
			return err
		}
	}
	if before != nil {
		// A ticket that left the column meanwhile cannot place it; go to the end.
		if next, err := loadTicket(tx, int64(*before)); err != nil || next.Col != api.ColumnReady {
			before = nil
		}
	}
	if err := b.relocate(tx, asker, t, api.ColumnReady, before, ActionApprove); err != nil {
		return err
	}
	return b.tellPM(tx, asker, t, fmt.Sprintf("Task #%d released by the user.", t.ID))
}

// refuse ends a gated move because plugin refused it. A refused start sends
// the ticket back to Planning, so the engine does not ask again for ever.
func (b *Board) refuse(tx *sql.Tx, n int64, m gateMoveRow, plugin, reason string) error {
	if err := b.gateEvent(tx, System, api.KindTicketRefused, n, m.move, plugin, reason, nil); err != nil {
		return err
	}
	if err := b.systemComment(tx, n, fmt.Sprintf("Plugin %s refused the move: %s", plugin, reason)); err != nil {
		return err
	}
	return b.endRequest(tx, n, m, ActionGateRefuse)
}

// endRequest drops a request without making its move. A start goes back to
// Planning (action says why); a release stays where it is.
func (b *Board) endRequest(tx *sql.Tx, n int64, m gateMoveRow, action string) error {
	t, err := loadTicket(tx, n)
	if err != nil {
		return err
	}
	if err := clearGates(tx, n); err != nil {
		return err
	}
	if m.move == api.GateMoveStart && t.Col == api.ColumnReady {
		return b.relocate(tx, System, t, api.ColumnPlanning, nil, action)
	}
	return b.emitUpdated(tx, System, n, "gate")
}

// GateOverride is the user's "continue anyway": every plugin that has not
// passed is treated as having given its go-ahead.
func (b *Board) GateOverride(ctx context.Context, a Actor, n int64) error {
	if a.Role != api.RoleUser {
		return api.Errorf(api.CodeForbidden, "only the user overrides a hold")
	}
	return b.write(ctx, func(tx *sql.Tx) error {
		m, _, err := openRequest(tx, n)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE ticket_gates SET state = 'passed', reason = '', choices = '[]', updated_at = ? WHERE ticket_id = ?`, b.stamp(), n); err != nil {
			return err
		}
		if err := b.gateEvent(tx, a, api.KindTicketOverridden, n, m.move, "", "", nil); err != nil {
			return err
		}
		return b.proceedIfPassed(tx, a, n, m, false)
	})
}

// GateCancel cancels the held move of ticket n: the user's own cancel
// (plugin "") or a plugin's, from one of its hooks. A cancelled release
// leaves the ticket in Planning; a cancelled start puts it back into
// Planning.
func (b *Board) GateCancel(ctx context.Context, a Actor, n int64, plugin string) error {
	if plugin == "" && a.Role != api.RoleUser {
		return api.Errorf(api.CodeForbidden, "only the user cancels a held move")
	}
	return b.write(ctx, func(tx *sql.Tx) error {
		m, rows, err := openRequest(tx, n)
		if err != nil {
			return err
		}
		if plugin != "" && gateOf(rows, plugin) == nil {
			return api.Errorf(api.CodeConflict, "plugin %s does not gate the move of #%d", plugin, n)
		}
		if err := b.gateEvent(tx, a, api.KindTicketHoldCanceled, n, m.move, plugin, "", nil); err != nil {
			return err
		}
		return b.endRequest(tx, n, m, ActionGateCancel)
	})
}

// GateNote posts a line on ticket n's thread as the system; the runner uses
// it for the plugins it cannot ask.
func (b *Board) GateNote(ctx context.Context, n int64, text string) error {
	return b.write(ctx, func(tx *sql.Tx) error {
		return b.systemComment(tx, n, cutRunes(text, maxGateNote))
	})
}
