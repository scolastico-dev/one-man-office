package engine

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
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
// slot, for failover. forced is set when the user pressed "Start anyway" on
// the ticket: its own model is then used although it is near its usage
// limit.
type modelChoice struct {
	ticketModel, previous string
	role                  settings.Role
	attempt               int
	forced                bool
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

// errUsageWait is the error of a start whose models exist but are all near
// their usage limits (or whose ticket's own model is): the agent waits
// until a model is below its limits again.
var errUsageWait = errors.New("every model it may use is near its usage limit")

// usageFunc tells how close a model is to its usage limits; nil means every
// model is unmetered.
type usageFunc func(id string) api.ModelUsage

// overSoft reports whether a model is at or over its soft threshold: no new
// agent starts on it.
func (use usageFunc) overSoft(id string) bool {
	if use == nil {
		return false
	}
	switch use(id).State {
	case api.UsageSoft, api.UsageHard:
		return true
	}
	return false
}

// startCheck says whether an agent could start now.
type startCheck int

const (
	// startOK: a model is available.
	startOK startCheck = iota
	// startUsage: models exist, but every one it may use is near its usage
	// limit; the start waits for usage.
	startUsage
	// startNoModel: no model exists for it; a person has to set one.
	startNoModel
)

// eligible reports whether id exists and is below its soft threshold.
func eligible(cat modelCatalog, use usageFunc, id string) bool {
	return exists(cat, id) && !use.overSoft(id)
}

// canStart reports whether pick would find a model for c, and if not, why
// not, without moving any cursor.
func canStart(c modelChoice, cat modelCatalog, use usageFunc) startCheck {
	if exists(cat, c.ticketModel) {
		if c.forced || !use.overSoft(c.ticketModel) {
			return startOK
		}
		return startUsage
	}
	found := false
	for _, id := range append([]string{c.previous}, c.role.Models...) {
		if !exists(cat, id) {
			continue
		}
		found = true
		if !use.overSoft(id) {
			return startOK
		}
	}
	if found {
		return startUsage
	}
	return startNoModel
}

// pick chooses the model of one agent start of role. Only models that exist
// and are below their soft usage threshold are eligible.
//
// The ticket's own model wins if it exists; when it is over its soft
// threshold the start waits for usage (errUsageWait) unless c.forced. Else
// the previous model of the slot is kept while it is eligible (on a crash
// restart, attempt > 0, a role in failover mode takes the next eligible
// model after the previous one in its list instead). Else the role's
// assignment chooses among the role's eligible models: round_robin takes
// the next eligible model from the role's cursor; random draws one eligible
// entry, so a model listed twice is twice as likely; failover takes the
// attempt-th distinct model, moving on over ineligible ones; smart takes the
// eligible model with the most capacity left (see pickSmart). Without any
// model that exists the error says which role file to fix; when models
// exist but none is eligible, the error is errUsageWait.
func (p *picker) pick(role api.Role, c modelChoice, cat modelCatalog, use usageFunc) (string, error) {
	if exists(cat, c.ticketModel) {
		if c.forced || !use.overSoft(c.ticketModel) {
			return c.ticketModel, nil
		}
		return "", errUsageWait
	}
	models := usable(c.role, cat)
	var ok []string
	for _, id := range models {
		if !use.overSoft(id) {
			ok = append(ok, id)
		}
	}
	failover := c.role.Assignment == api.AssignmentFailover && len(ok) > 0
	if exists(cat, c.previous) {
		if failover && c.attempt > 0 {
			// A crash restart of a role in failover mode moves on to the
			// eligible model after the one that crashed.
			distinct := distinctModels(models)
			start := c.attempt
			if i := slices.Index(distinct, c.previous); i >= 0 {
				start = i + 1
			}
			return nextEligible(distinct, start, use), nil
		}
		if !use.overSoft(c.previous) {
			return c.previous, nil
		}
		// The previous model is near its limit: the role's assignment
		// decides as for a fresh agent.
	}
	if len(models) == 0 {
		if exists(cat, c.previous) {
			return "", errUsageWait
		}
		return "", noModelError(role)
	}
	if len(ok) == 0 {
		return "", errUsageWait
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch c.role.Assignment {
	case api.AssignmentRandom:
		i := p.rand(len(ok))
		return ok[min(max(i, 0), len(ok)-1)], nil
	case api.AssignmentFailover:
		return nextEligible(distinctModels(models), max(c.attempt, 0), use), nil
	case api.AssignmentSmart:
		return p.pickSmart(role, distinctModels(ok), use), nil
	default: // round_robin and anything unknown
		return p.next(role, models, use), nil
	}
}

// nextEligible returns the first eligible model of list from index start
// on, wrapping around. list holds at least one eligible model.
func nextEligible(list []string, start int, use usageFunc) string {
	for k := range list {
		if id := list[(start+k)%len(list)]; !use.overSoft(id) {
			return id
		}
	}
	return list[start%len(list)]
}

// next takes the next eligible entry of models from role's round-robin
// cursor and moves the cursor past it. p.mu is held.
func (p *picker) next(role api.Role, models []string, use usageFunc) string {
	if p.cursor == nil {
		p.cursor = map[api.Role]int{}
	}
	i := p.cursor[role] % len(models)
	for k := range models {
		j := (i + k) % len(models)
		if !use.overSoft(models[j]) {
			p.cursor[role] = j + 1
			return models[j]
		}
	}
	p.cursor[role] = i + 1
	return models[i]
}

// pickSmart chooses among the role's eligible models (ok, distinct, in the
// role's order): when none has a reading it is round robin; otherwise the
// metered model with the lowest usage (the first listed on a tie) takes
// turns with the unmetered and unknown models through the role's cursor.
// Repeats in the role's list do not matter. p.mu is held.
func (p *picker) pickSmart(role api.Role, ok []string, use usageFunc) string {
	best, bestUsed := "", 0.0
	var turns []string
	for _, id := range ok {
		u := api.ModelUsage{State: api.UsageUnmetered}
		if use != nil {
			u = use(id)
		}
		if u.State == api.UsageOK && u.UsedPercent != nil {
			if best == "" || *u.UsedPercent < bestUsed {
				best, bestUsed = id, *u.UsedPercent
			}
			continue
		}
		turns = append(turns, id)
	}
	if best == "" {
		return p.next(role, ok, use)
	}
	// The best metered model keeps its place in the role's order.
	var list []string
	for _, id := range ok {
		if id == best || slices.Contains(turns, id) {
			list = append(list, id)
		}
	}
	return p.next(role, list, use)
}

// use returns how close a model is to its usage limits; without a usage
// view every model is unmetered.
func (e *Engine) use(id string) api.ModelUsage {
	if e.o.Usage == nil {
		return api.ModelUsage{Model: id, State: api.UsageUnmetered}
	}
	return e.o.Usage.Model(id)
}

// hasModel reports whether an agent of role with purpose on t could start
// now: the ticket's own model (for its working agent) or one of the role's
// below its soft usage threshold. The model of an earlier session is not
// counted, so a role without models is flagged even while an old model
// would still be reused.
func (e *Engine) hasModel(role api.Role, purpose api.Purpose, t board.EngineTicket) startCheck {
	c := e.modelChoice(role, purpose, t, "", 0)
	c.forced = c.ticketModel != "" && e.isForced(t)
	return canStart(c, e.st, e.use)
}
