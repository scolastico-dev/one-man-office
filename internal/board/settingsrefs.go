package board

import (
	"context"
	"database/sql"
)

// ProjectIDsInUse returns every project id any ticket references, Done and
// cancelled tickets included.
//
// It is not scoped to an actor: call it only from user-only routes and from
// the settings store's callback, never on behalf of an agent session.
func (b *Board) ProjectIDsInUse(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT DISTINCT project_id FROM ticket_projects`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out[id] = true
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// OpenTicketsWithProject lists the numbers, ascending, of the open (not Done)
// tickets that list project id. The result is never nil.
//
// It is not scoped to an actor: call it only from user-only routes and from
// the settings store's callback, never on behalf of an agent session.
func (b *Board) OpenTicketsWithProject(ctx context.Context, id string) ([]int64, error) {
	out := []int64{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT t.id FROM tickets t
			JOIN ticket_projects p ON p.ticket_id = t.id
			WHERE p.project_id = ? AND t.col <> 'done'
			ORDER BY t.id`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n int64
			if err := rows.Scan(&n); err != nil {
				return err
			}
			out = append(out, n)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// OpenTicketsWithModels maps each given model id to the numbers, ascending,
// of the open (not Done) tickets that set it as their model. Ids no open
// ticket uses, and the empty id, are left out. The result is never nil.
//
// It is not scoped to an actor: call it only from user-only routes and from
// the settings store's callback, never on behalf of an agent session.
func (b *Board) OpenTicketsWithModels(ctx context.Context, ids []string) (map[string][]int64, error) {
	out := map[string][]int64{}
	want := make([]any, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		want = append(want, id)
	}
	if len(want) == 0 {
		return out, nil
	}
	err := b.read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id, model FROM tickets
			WHERE col <> 'done' AND model IN (`+placeholders(len(want))+`)
			ORDER BY id`, want...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n int64
			var model string
			if err := rows.Scan(&n, &model); err != nil {
				return err
			}
			out[model] = append(out[model], n)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RecordedBranch is the branch recorded for Ticket in Project.
type RecordedBranch struct {
	Branch  string
	Ticket  int64
	Project string
}

// RecordedBranches returns the branches recorded on any ticket in any
// project, done ones included, ordered by ticket: a branch may be deleted by
// now, and its name still belongs to that ticket. Branch names belong to a
// repository and several projects can lie in one (folders of a monorepo),
// so the caller picks the projects that matter. Like OpenTicketsWithProject
// it is not scoped to an actor; the engine calls it when it picks a branch
// name and before it pushes or deletes a ticket's branch.
func (b *Board) RecordedBranches(ctx context.Context) ([]RecordedBranch, error) {
	var out []RecordedBranch
	err := b.read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT branch, ticket_id, project_id FROM ticket_projects WHERE branch <> '' ORDER BY ticket_id, project_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r RecordedBranch
			if err := rows.Scan(&r.Branch, &r.Ticket, &r.Project); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}
