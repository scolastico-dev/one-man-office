package board

import "github.com/scolastico-dev/one-man-office/internal/api"

// columnWords names a column as the board's title does. Refusal texts are
// read by the user and by agents who know the board by its titles, so they
// never carry the identifier (which only API calls use).
func columnWords(c api.Column) string {
	switch c {
	case api.ColumnPlanning:
		return "Planning"
	case api.ColumnReady:
		return "Ready For Dev"
	case api.ColumnInProgress:
		return "In Progress"
	case api.ColumnReview:
		return "Review"
	case api.ColumnDone:
		return "Done"
	}
	return string(c)
}
