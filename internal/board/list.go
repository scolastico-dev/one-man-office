package board

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// Done column paging.
const (
	defaultDoneLimit = 50
	maxDoneLimit     = 500
)

// columnOrder sorts the open columns in board order.
const columnOrder = `CASE t.col WHEN 'planning' THEN 0 WHEN 'ready' THEN 1 WHEN 'in_progress' THEN 2 ELSE 3 END`

// List returns the tickets matching f that a may read, together with the id
// of the last event at snapshot time. Open tickets come in board order (column, then rank);
// Done tickets follow, most recently closed first, paged by DoneLimit
// (default 50) and DoneBefore (the last ticket of the previous page).
func (b *Board) List(ctx context.Context, a Actor, f api.TicketFilter) (api.TicketList, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return api.TicketList{}, err
	}
	conds, args, err := listConditions(a, f)
	if err != nil {
		return api.TicketList{}, err
	}
	// Every append below starts a new slice.
	conds, args = slices.Clip(conds), slices.Clip(args)
	limit := f.DoneLimit
	if limit <= 0 {
		limit = defaultDoneLimit
	}
	limit = min(limit, maxDoneLimit)

	out := api.TicketList{Tickets: []api.TicketSummary{}}
	err = b.read(ctx, func(tx *sql.Tx) error {
		var err error
		if out.EventID, err = db.LastEventID(tx); err != nil {
			return err
		}
		if f.Column != api.ColumnDone {
			open, err := b.summaries(tx, strings.Join(append(conds, `t.col <> 'done'`), ` AND `)+
				` ORDER BY `+columnOrder+`, t.rank, t.id`, args...)
			if err != nil {
				return err
			}
			out.Tickets = append(out.Tickets, open...)
		}
		if f.Column != "" && f.Column != api.ColumnDone {
			return nil
		}
		doneConds := append(conds, `t.col = 'done'`)
		if err := tx.QueryRow(`SELECT COUNT(*) FROM tickets t WHERE `+strings.Join(doneConds, ` AND `), args...).Scan(&out.DoneTotal); err != nil {
			return err
		}
		doneArgs := slices.Clip(args)
		if f.DoneBefore > 0 {
			if err := checkRead(tx, a, int64(f.DoneBefore)); err != nil {
				return err
			}
			var closed sql.NullString
			err := tx.QueryRow(`SELECT closed_at FROM tickets WHERE id = ?`, f.DoneBefore).Scan(&closed)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && !closed.Valid) {
				return api.Errorf(api.CodeValidation, "done_before #%d is not a done ticket", f.DoneBefore)
			}
			if err != nil {
				return err
			}
			doneConds = append(doneConds, `(t.closed_at < ? OR (t.closed_at = ? AND t.id < ?))`)
			doneArgs = append(append([]any{}, args...), closed.String, closed.String, f.DoneBefore)
		}
		done, err := b.summaries(tx, strings.Join(doneConds, ` AND `)+
			` ORDER BY t.closed_at DESC, t.id DESC LIMIT ?`, append(doneArgs, limit+1)...)
		if err != nil {
			return err
		}
		if len(done) > limit {
			done = done[:limit]
			out.NextDoneBefore = done[limit-1].Number
		}
		out.Tickets = append(out.Tickets, done...)
		return nil
	})
	return forActor(a, out, err)
}

// listConditions turns a filter into SQL conditions on tickets t, limited
// to the tickets a may read.
func listConditions(a Actor, f api.TicketFilter) ([]string, []any, error) {
	conds := []string{`1 = 1`}
	var args []any
	if cond, scopeArgs, all := readScope(a); !all {
		conds = append(conds, cond)
		args = append(args, scopeArgs...)
	}
	switch f.Column {
	case "", api.ColumnDone:
	case api.ColumnPlanning, api.ColumnReady, api.ColumnInProgress, api.ColumnReview:
		conds = append(conds, `t.col = ?`)
		args = append(args, f.Column)
	default:
		return nil, nil, api.Errorf(api.CodeValidation, "unknown column %q", f.Column)
	}
	switch f.Type {
	case "":
	case api.TypeStory, api.TypeTask:
		conds = append(conds, `t.type = ?`)
		args = append(args, f.Type)
	default:
		return nil, nil, api.Errorf(api.CodeValidation, "unknown ticket type %q", f.Type)
	}
	if f.Project != "" {
		conds = append(conds, `EXISTS (SELECT 1 FROM ticket_projects tp WHERE tp.ticket_id = t.id AND tp.project_id = ?)`)
		args = append(args, f.Project)
	}
	if f.Story != nil {
		conds = append(conds, `(t.id = ? OR t.story_id = ?)`)
		args = append(args, *f.Story, *f.Story)
	}
	switch f.Supervisor {
	case "":
	case api.RoleUser, api.RoleCEO, api.RolePM:
		conds = append(conds, `t.supervisor = ?`)
		args = append(args, f.Supervisor)
	default:
		return nil, nil, api.Errorf(api.CodeValidation, "supervisor must be user, ceo or pm, not %q", f.Supervisor)
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		number := int64(-1)
		if n, err := strconv.ParseInt(strings.TrimPrefix(q, "#"), 10, 64); err == nil {
			number = n
		}
		conds = append(conds, `(t.id = ? OR instr(lower(t.title), lower(?)) > 0 OR EXISTS (
			SELECT 1 FROM description_versions dv WHERE dv.ticket_id = t.id AND dv.number = t.description_version
			AND instr(lower(dv.body), lower(?)) > 0))`)
		args = append(args, number, q, q)
	}
	if f.NeedsMe {
		cond := `(EXISTS (SELECT 1 FROM comments c WHERE c.ticket_id = t.id AND c.question_to = ? AND c.answered_by IS NULL)
			OR (t.attention <> '' AND t.supervisor = ?)`
		if a.Role == api.RoleUser {
			cond += ` OR t.review_stage = 'human'`
		}
		conds = append(conds, cond+`)`)
		args = append(args, a.Role, a.Role)
		if a.Role == api.RolePM {
			// A product manager answers only for its story and its tasks.
			conds = append(conds, `(t.id = ? OR t.story_id = ?)`)
			args = append(args, a.Ticket, a.Ticket)
		}
	}
	return conds, args, nil
}
