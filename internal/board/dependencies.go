package board

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// dependencyRule names who may change a ticket's dependencies.
const dependencyRule = "the user changes every ticket, the CEO tickets it created, a product manager the tasks of its story"

// mayLink reports whether a may add or remove dependencies of t.
func mayLink(a Actor, t ticketRow) bool {
	switch a.Role {
	case api.RoleUser:
		return true
	case api.RoleCEO:
		return t.CreatedByRole == api.RoleCEO
	case api.RolePM:
		return t.inStory() && t.StoryID == a.Ticket
	}
	return false
}

// AddDependency makes ticket n depend on dependsOn. A dependency that
// already exists changes nothing.
func (b *Board) AddDependency(ctx context.Context, a Actor, n, dependsOn int64) (api.Ticket, error) {
	return b.changeDependency(ctx, a, n, dependsOn, true)
}

// RemoveDependency removes the dependency of ticket n on dependsOn. A
// dependency that does not exist changes nothing.
func (b *Board) RemoveDependency(ctx context.Context, a Actor, n, dependsOn int64) (api.Ticket, error) {
	return b.changeDependency(ctx, a, n, dependsOn, false)
}

func (b *Board) changeDependency(ctx context.Context, a Actor, n, dependsOn int64, add bool) (api.Ticket, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return api.Ticket{}, err
	}
	var out api.Ticket
	err := b.write(ctx, func(tx *sql.Tx) error {
		for _, id := range []int64{n, dependsOn} {
			if err := checkRead(tx, a, id); err != nil {
				return err
			}
		}
		from, err := loadTicket(tx, n)
		if err != nil {
			return err
		}
		to, err := loadTicket(tx, dependsOn)
		if err != nil {
			return err
		}
		if !mayLink(a, from) {
			return api.Errorf(api.CodeForbidden, "a %s cannot change the dependencies of #%d: %s", a.Role, n, dependencyRule)
		}
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM dependencies WHERE ticket_id = ? AND depends_on_id = ?`,
			n, dependsOn).Scan(&exists); err != nil {
			return err
		}
		kind := api.KindDependencyAdded
		switch {
		case add && exists == 0:
			if err := checkDependency(tx, from, to); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO dependencies (ticket_id, depends_on_id, created_at) VALUES (?, ?, ?)`,
				n, dependsOn, b.stamp()); err != nil {
				return err
			}
		case !add && exists == 1:
			if from.Col == api.ColumnDone {
				return api.Errorf(api.CodeInvalidTransition, "#%d is done; done tickets are read-only", n)
			}
			kind = api.KindDependencyRemoved
			if _, err := tx.Exec(`DELETE FROM dependencies WHERE ticket_id = ? AND depends_on_id = ?`,
				n, dependsOn); err != nil {
				return err
			}
		default:
			out, err = b.ticket(tx, n)
			return err
		}
		if err := emit(tx, kind, n, a, api.DependencyData{Ticket: int(n), DependsOn: int(dependsOn)}); err != nil {
			return err
		}
		// Both ends changed: depends_on and waiting_for of one, blocks of
		// the other.
		if err := b.touch(tx, a, n, dependsOn); err != nil {
			return err
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return out, err
}

// linkNewTicket stores the dependencies of a ticket that was just inserted.
// Every link goes through checkDependency, like a link added later.
func linkNewTicket(tx db.Queryer, id int64, deps []int64, now string) error {
	if len(deps) == 0 {
		return nil
	}
	from, err := loadTicket(tx, id)
	if err != nil {
		return err
	}
	for _, dep := range deps {
		to, err := loadTicket(tx, dep)
		if err != nil {
			return err
		}
		if err := checkDependency(tx, from, to); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO dependencies (ticket_id, depends_on_id, created_at) VALUES (?, ?, ?)`,
			id, dep, now); err != nil {
			return err
		}
	}
	return nil
}

// checkDependency applies every rule for a new link "from depends on to":
// not itself, the placement rule, not on a cancelled ticket, not from a
// done ticket, and no cycle.
func checkDependency(q db.Queryer, from, to ticketRow) error {
	if from.ID == to.ID {
		return api.Errorf(api.CodeDependencyScope, "#%d cannot depend on itself", from.ID)
	}
	if err := dependencyScope(from, to); err != nil {
		return err
	}
	if to.Resolution == api.ResolutionCancelled {
		return api.Errorf(api.CodeDependencyScope, "#%d cannot depend on cancelled #%d", from.ID, to.ID)
	}
	if from.Col == api.ColumnDone {
		return api.Errorf(api.CodeDependencyScope, "#%d is done and cannot get a new dependency", from.ID)
	}
	cycle, err := findCycle(q, from.ID, to.ID)
	if err != nil {
		return err
	}
	if cycle != nil {
		// The chain runs from to back to from, which closes the cycle.
		parts := make([]string, 0, len(cycle)+1)
		parts = append(parts, fmt.Sprintf("#%d", from.ID))
		for _, id := range cycle {
			parts = append(parts, fmt.Sprintf("#%d", id))
		}
		return api.Errorf(api.CodeDependencyCycle, "#%d cannot depend on #%d: that would be a cycle %s",
			from.ID, to.ID, strings.Join(parts, " → "))
	}
	return nil
}

// findCycle reports whether "from depends on to" would close a cycle, that
// is whether to already depends on from, directly or through other tickets.
// It returns the chain of dependencies from to to from, or nil when there is
// no cycle. One recursive query walks the whole chain, however long.
func findCycle(q db.Queryer, from, to int64) ([]int64, error) {
	if from == to {
		return []int64{from}, nil
	}
	parent := map[int64]int64{}
	err := each(q, `WITH RECURSIVE reach(id, parent) AS (
			SELECT ?1, 0
			UNION
			SELECT d.depends_on_id, r.id FROM dependencies d JOIN reach r ON d.ticket_id = r.id
		) SELECT id, parent FROM reach`, to, func(rows *sql.Rows) error {
		var id, p int64
		if err := rows.Scan(&id, &p); err != nil {
			return err
		}
		if _, ok := parent[id]; !ok {
			parent[id] = p
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := parent[from]; !ok {
		return nil, nil
	}
	// Walk back from "from" to "to"; the stored links are acyclic, and the
	// bound guards against a damaged database.
	var rev []int64
	for id := from; len(rev) <= len(parent); id = parent[id] {
		rev = append(rev, id)
		if id == to {
			break
		}
	}
	chain := make([]int64, len(rev))
	for i, id := range rev {
		chain[len(rev)-1-i] = id
	}
	return chain, nil
}

// checkLinkScopes checks every existing dependency of t, in both
// directions, against the placement rule after t changed its story.
func checkLinkScopes(q db.Queryer, t ticketRow) error {
	type link struct{ from, to int64 }
	var links []link
	if err := each(q, `SELECT ticket_id, depends_on_id FROM dependencies WHERE ticket_id = ?1 OR depends_on_id = ?1
		ORDER BY ticket_id, depends_on_id`, t.ID, func(rows *sql.Rows) error {
		var l link
		if err := rows.Scan(&l.from, &l.to); err != nil {
			return err
		}
		links = append(links, l)
		return nil
	}); err != nil {
		return err
	}
	load := func(n int64) (ticketRow, error) {
		if n == t.ID {
			return t, nil
		}
		return loadTicket(q, n)
	}
	for _, l := range links {
		from, err := load(l.from)
		if err != nil {
			return err
		}
		to, err := load(l.to)
		if err != nil {
			return err
		}
		if err := dependencyScope(from, to); err != nil {
			return err
		}
	}
	return nil
}

// dependencyScope applies the placement rule for "from depends on to": a
// task in a story only on tasks of the same story, a story only on stories,
// a standalone task on standalone tasks or stories. It is the only place
// that rule lives; new links go through checkDependency and links that
// exist while a task changes its story through checkLinkScopes.
func dependencyScope(from, to ticketRow) error {
	var ok bool
	var rule string
	switch {
	case from.inStory():
		ok = to.inStory() && to.StoryID == from.StoryID
		rule = "a task in a story depends only on tasks of the same story"
	case from.isStory():
		ok = to.isStory()
		rule = "a story depends only on stories"
	default:
		ok = to.isStory() || to.standalone()
		rule = "a standalone task depends only on standalone tasks or stories"
	}
	if !ok {
		return api.Errorf(api.CodeDependencyScope, "#%d cannot depend on #%d: %s", from.ID, to.ID, rule)
	}
	return nil
}
