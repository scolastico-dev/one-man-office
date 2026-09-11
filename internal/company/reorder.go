package company

import "github.com/scolastico-dev/one-man-office/internal/globalhome"

// ReorderProjects persists a new order for every currently trusted office and
// returns the refreshed project list.
func ReorderProjects(paths []string) ([]Project, error) {
	home, err := globalhome.Open()
	if err != nil {
		return nil, err
	}
	if err := home.Reorder(paths); err != nil {
		return nil, err
	}
	return Projects()
}
