package board

import (
	"database/sql"
	"errors"
	"strconv"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// rankStep is the rank distance between a column's last ticket and a ticket
// appended after it, and between neighbours after a renumbering.
const rankStep = 1024

// minRankGap is the smallest distance two neighbouring ranks may have. A
// placement that would leave a smaller gap renumbers the column first.
const minRankGap = 1e-9

// endRank returns the rank that places a ticket at the end of a column.
func endRank(q db.Queryer, col api.Column) (float64, error) {
	return endRankExcept(q, col, 0)
}

// endRankExcept is endRank ignoring ticket self (0 = none), which is about
// to be placed.
func endRankExcept(q db.Queryer, col api.Column, self int64) (float64, error) {
	var rank float64
	err := q.QueryRow(`SELECT COALESCE(MAX(rank), 0) + ? FROM tickets WHERE col = ? AND id <> ?`,
		rankStep, col, self).Scan(&rank)
	return rank, err
}

// placeRank returns the rank that puts ticket self into col directly above
// ticket before, or at the end of col when before is nil. Card order is
// rank, then number. When the gap above before has become too small, the
// column is renumbered first (event column.renumbered). The second result is
// false when before is self, which leaves the ticket where it is.
func (b *Board) placeRank(tx *sql.Tx, a Actor, col api.Column, before *int, self int64) (float64, bool, error) {
	if before == nil {
		r, err := endRankExcept(tx, col, self)
		return r, true, err
	}
	n := int64(*before)
	for attempt := 0; ; attempt++ {
		next, err := loadTicket(tx, n)
		if errCode(err) == api.CodeNotFound {
			return 0, false, api.Errorf(api.CodeValidation, "before: ticket #%d does not exist", n)
		}
		if err != nil {
			return 0, false, err
		}
		if next.Col != col {
			return 0, false, api.Errorf(api.CodeValidation,
				"before: #%d is in %s, not in %s; name a ticket of the target column or null for its end", n, next.Col, col)
		}
		if n == self {
			return 0, false, nil
		}
		var prev float64
		err = tx.QueryRow(`SELECT rank FROM tickets WHERE col = ? AND id <> ? AND id <> ?
			AND (rank < ? OR (rank = ? AND id < ?)) ORDER BY rank DESC, id DESC LIMIT 1`,
			col, self, n, next.Rank, next.Rank, n).Scan(&prev)
		if errors.Is(err, sql.ErrNoRows) {
			prev = next.Rank - rankStep
		} else if err != nil {
			return 0, false, err
		}
		mid := prev + (next.Rank-prev)/2
		if (next.Rank-prev)/2 >= minRankGap && mid > prev && mid < next.Rank {
			return mid, true, nil
		}
		if attempt > 0 {
			return 0, false, errors.New("rank: no gap after renumbering")
		}
		if err := renumber(tx, a, col, self); err != nil {
			return 0, false, err
		}
	}
}

// renumber gives the tickets of col, except self, the ranks 1024, 2048, …
// in card order and emits column.renumbered.
func renumber(tx *sql.Tx, a Actor, col api.Column, self int64) error {
	var ids []int64
	if err := each(tx, `SELECT id FROM tickets WHERE col = ? AND id <> `+strconv.FormatInt(self, 10)+
		` ORDER BY rank, id`, col, func(rows *sql.Rows) error {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	}); err != nil {
		return err
	}
	ranks := make(map[string]float64, len(ids))
	for i, id := range ids {
		r := float64((i + 1) * rankStep)
		if _, err := tx.Exec(`UPDATE tickets SET rank = ? WHERE id = ?`, r, id); err != nil {
			return err
		}
		ranks[strconv.FormatInt(id, 10)] = r
	}
	return emit(tx, api.KindColumnRenumbered, 0, a, api.ColumnRenumberedData{Column: col, Ranks: ranks})
}
