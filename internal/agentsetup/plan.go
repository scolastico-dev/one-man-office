package agentsetup

import (
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Found is an agent program that lookPath found.
type Found struct{ CLI, Command, Path string }

// Plan is what detection would do. Nothing in it has been written.
type Plan struct {
	Found    []Found
	Profiles []api.Profile      // to write; never one whose name or CLI type exists
	Roles    []api.RoleSettings // only roles that have no models now
	Skipped  []string           // one sentence each
}

// roleOrder is the order the roles appear in a plan.
var roleOrder = []string{"ceo", "pm", "developer", "reviewer"}

// Detect looks for the catalog's programs through lookPath, in the order of
// agentcli.Supported, and plans a profile for each one whose CLI type has no
// profile yet and whose catalog name is free. It then plans models for the
// roles that have none: the CEO gets the strongest model of each new profile
// (failover); the PM and the reviewer the strongest selectable one and the
// developer the middle one of the selectable models, the stronger of two in
// the middle. PM/developer use super_smart and reviewer uses random. A role that
// has models is never in the plan. Roles missing from roles count as empty.
// A role's configured super smart reset weight is kept when models are added.
// It never runs a program it finds.
func Detect(lookPath func(string) (string, error), existing []api.Profile, roles []api.RoleSettings) Plan {
	var plan Plan
	for _, provider := range agentcli.Supported {
		cli := string(provider)
		e, ok := entry(cli)
		if !ok {
			continue
		}
		path, err := lookPath(e.command)
		if err != nil {
			continue
		}
		plan.Found = append(plan.Found, Found{CLI: cli, Command: e.command, Path: path})
		if name, taken := profileOfCLI(existing, cli); taken {
			plan.Skipped = append(plan.Skipped, fmt.Sprintf("Found %s, but the profile %s already uses the %s CLI, so no profile was added.", e.command, name, cli))
			continue
		}
		if profileNamed(existing, e.cli) {
			plan.Skipped = append(plan.Skipped, fmt.Sprintf("Found %s, but a profile named %s already exists, so no profile was added.", e.command, e.cli))
			continue
		}
		p, _ := Catalog(cli)
		plan.Profiles = append(plan.Profiles, p)
	}
	if len(plan.Profiles) == 0 {
		return plan
	}
	for _, name := range roleOrder {
		if roleHasModels(roles, name) {
			continue
		}
		r := api.RoleSettings{Name: name, Assignment: api.DefaultAssignment(name)}
		for _, current := range roles {
			if current.Name == name && current.SuperSmartResetWeight != nil {
				weight := *current.SuperSmartResetWeight
				r.SuperSmartResetWeight = &weight
				break
			}
		}
		for _, p := range plan.Profiles {
			if id, ok := pick(p, name); ok {
				r.Models = append(r.Models, id)
			}
		}
		if len(r.Models) > 0 {
			plan.Roles = append(plan.Roles, r)
		}
	}
	return plan
}

// pick chooses the model id of a new profile for a role.
func pick(p api.Profile, role string) (string, bool) {
	order := Strength(p.CLI)
	if role != "ceo" {
		var selectable []string
		for _, m := range order {
			if s := p.Models[m].Selectable; s == nil || *s {
				selectable = append(selectable, m)
			}
		}
		order = selectable
	}
	if len(order) == 0 {
		return "", false
	}
	i := 0
	if role == "developer" {
		// The middle one; of two in the middle the stronger.
		i = (len(order) - 1) / 2
	}
	return p.Name + "/" + order[i], true
}

func profileOfCLI(existing []api.Profile, cli string) (string, bool) {
	for _, p := range existing {
		if p.CLI == cli {
			return p.Name, true
		}
	}
	return "", false
}

func profileNamed(existing []api.Profile, name string) bool {
	for _, p := range existing {
		if p.Name == name {
			return true
		}
	}
	return false
}

func roleHasModels(roles []api.RoleSettings, name string) bool {
	for _, r := range roles {
		if r.Name == name && len(r.Models) > 0 {
			return true
		}
	}
	return false
}
