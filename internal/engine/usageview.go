package engine

import "github.com/scolastico-dev/one-man-office/internal/api"

// UsageView tells the engine how close a model is to its usage limits. nil in
// Options means every model is unmetered.
type UsageView interface {
	Model(id string) api.ModelUsage
}
