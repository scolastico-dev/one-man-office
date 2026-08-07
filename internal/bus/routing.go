// Package bus is the office message system: one flat messages table plus the
// enforced routing matrix from the spec.
package bus

import "fmt"

type Directory interface {
	Role(agent string) (string, bool)
	LivingAgents() []string
	AgentsByRole(role string) []string
	CEO() (string, bool)
	PMOf(developer string) (string, bool)
	DevelopersOf(pm string) []string
	ReviewTargets(reviewer string) (developer, pm string, ok bool)
	ReviewerOf(developer string) (reviewer string, ok bool)
}

// Allowed enforces the routing matrix. target is an agent name, a role name,
// "user", or "" (broadcast to all living agents).
func Allowed(d Directory, from, target string) error {
	if from == "user" {
		return nil
	}
	fromRole, ok := d.Role(from)
	if !ok {
		return fmt.Errorf("unknown sender %q", from)
	}
	if fromRole == "ceo" || fromRole == "firefighter" {
		return nil
	}
	if target == "" {
		return fmt.Errorf("%s may not broadcast", fromRole)
	}
	// Emergency channel: the CEO is always reachable, by name or role.
	if target == "ceo" {
		return nil
	}
	if ceo, ok := d.CEO(); ok && target == ceo {
		return nil
	}
	if target == "user" {
		return fmt.Errorf("%s may not message the user", fromRole)
	}
	switch fromRole {
	case "developer":
		if pm, ok := d.PMOf(from); ok && target == pm {
			return nil
		}
		if reviewer, ok := d.ReviewerOf(from); ok && target == reviewer {
			return nil
		}
	case "product_manager":
		if target == "product_manager" {
			return nil // lateral role-address: all other PMs
		}
		if r, ok := d.Role(target); ok && r == "product_manager" && target != from {
			return nil
		}
		for _, dev := range d.DevelopersOf(from) {
			if target == dev {
				return nil
			}
		}
		if r, ok := d.Role(target); ok && r == "reviewer" {
			if _, pm, related := d.ReviewTargets(target); related && pm == from {
				return nil
			}
		}
	case "reviewer":
		if dev, pm, ok := d.ReviewTargets(from); ok && (target == dev || (pm != "" && target == pm)) {
			return nil
		}
	}
	return fmt.Errorf("routing: %s (%s) may not send to %q", from, fromRole, target)
}
