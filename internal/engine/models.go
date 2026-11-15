package engine

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// modelChoice is what decides the model of one agent start.
//
// ticketModel is the ticket's own model ("" = none); it is used for the
// working agent of a ticket only, so the caller leaves it empty for
// reviewers and export jobs. previous is the model of the newest earlier
// session of the same agent slot, so that a fresh agent after a crash or a
// restart keeps its model; also empty for reviewers and export jobs. role is
// the role's assignment. attempt counts the consecutive failed starts of the
// slot, for failover.
type modelChoice struct {
	ticketModel, previous string
	role                  settings.Role
	attempt               int
}

// modelCatalog tells which models exist; *settings.Store is one.
type modelCatalog interface {
	Model(id string) (settings.ModelInfo, bool)
}

// picker picks models. It keeps one round-robin cursor per role, in memory;
// rand returns a number in [0, n) for the random mode.
type picker struct {
	mu     sync.Mutex
	cursor map[api.Role]int
	rand   func(n int) int
}

// newPicker returns a picker using rnd for the random mode, or math/rand
// when rnd is nil.
func newPicker(rnd func(n int) int) *picker {
	if rnd == nil {
		rnd = rand.IntN
	}
	return &picker{cursor: map[api.Role]int{}, rand: rnd}
}

// noModelError is the error of a role without any usable model.
func noModelError(role api.Role) error {
	return fmt.Errorf("%s", noModelText(role))
}

// noModelText says that role has no usable model and where to set one.
func noModelText(role api.Role) string {
	return fmt.Sprintf("no usable model for the %s role; set models in roles/%s.yaml", role, role)
}

// exists reports whether id names a model of the catalog.
func exists(cat modelCatalog, id string) bool {
	if id == "" || cat == nil {
		return false
	}
	_, ok := cat.Model(id)
	return ok
}

// usable returns the role's models that exist, in order and with repeats.
func usable(r settings.Role, cat modelCatalog) []string {
	var out []string
	for _, id := range r.Models {
		if exists(cat, id) {
			out = append(out, id)
		}
	}
	return out
}

// hasUsableModel reports whether pick would find a model for c.
func hasUsableModel(c modelChoice, cat modelCatalog) bool {
	return exists(cat, c.ticketModel) || exists(cat, c.previous) || len(usable(c.role, cat)) > 0
}

// distinctModels returns models without repeats, in order.
func distinctModels(models []string) []string {
	var out []string
	for _, m := range models {
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// pick chooses the model of one agent start of role: the ticket's own model
// if it exists, else the previous model of the slot if it exists (on a
// crash restart, attempt > 0, a role in failover mode takes the model after
// the previous one in its list instead), else the
// role's assignment over the role's models that exist. round_robin (and
// smart, until usage data exists) takes the next model of the role's
// cursor; random draws one entry, so a model listed twice is twice as
// likely; failover takes the attempt-th distinct model, starting over after
// the last. Without any usable model the error says which role file to fix.
func (p *picker) pick(role api.Role, c modelChoice, cat modelCatalog) (string, error) {
	if exists(cat, c.ticketModel) {
		return c.ticketModel, nil
	}
	models := usable(c.role, cat)
	failover := c.role.Assignment == api.AssignmentFailover && len(models) > 0
	if exists(cat, c.previous) {
		if !failover || c.attempt <= 0 {
			return c.previous, nil
		}
		// A crash restart of a role in failover mode moves on to the
		// model after the one that crashed.
		distinct := distinctModels(models)
		if i := slices.Index(distinct, c.previous); i >= 0 {
			return distinct[(i+1)%len(distinct)], nil
		}
		return distinct[c.attempt%len(distinct)], nil
	}
	if len(models) == 0 {
		return "", noModelError(role)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch c.role.Assignment {
	case api.AssignmentRandom:
		i := p.rand(len(models))
		return models[min(max(i, 0), len(models)-1)], nil
	case api.AssignmentFailover:
		distinct := distinctModels(models)
		return distinct[max(c.attempt, 0)%len(distinct)], nil
	default: // round_robin, smart, and anything unknown
		if p.cursor == nil {
			p.cursor = map[api.Role]int{}
		}
		i := p.cursor[role] % len(models)
		p.cursor[role] = i + 1
		return models[i], nil
	}
}
