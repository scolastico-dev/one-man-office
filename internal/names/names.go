// Package names assigns human-friendly agent names: role prefix plus a
// random first name from an embedded list, collision-checked per office.
package names

import (
	_ "embed"
	"fmt"
	"math/rand/v2"
	"strings"
)

//go:embed firstnames.txt
var firstnamesFile string

var Prefix = map[string]string{
	"ceo":             "ceo",
	"product_manager": "pm",
	"developer":       "developer",
	"reviewer":        "reviewer",
	"freelancer":      "freelancer",
	"smokealarm":      "smokealarm",
	"firefighter":     "firefighter",
	"branch_namer":    "branch-namer",
}

func firstnames() []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(firstnamesFile), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Pick returns "<prefix>-<firstname>" such that taken(name) is false.
func Pick(role string, taken func(string) bool) (string, error) {
	prefix, ok := Prefix[role]
	if !ok {
		return "", fmt.Errorf("unknown role %q", role)
	}
	list := firstnames()
	start := rand.IntN(len(list))
	for i := 0; i < len(list); i++ {
		n := prefix + "-" + list[(start+i)%len(list)]
		if !taken(n) {
			return n, nil
		}
	}
	// Historical names remain reserved for transcript identity. Reuse a free
	// first-name stem with a suffix once the plain form has appeared before.
	for suffix := 2; suffix < 10000; suffix++ {
		for i := 0; i < len(list); i++ {
			n := fmt.Sprintf("%s-%s-%d", prefix, list[(start+i)%len(list)], suffix)
			if !taken(n) {
				return n, nil
			}
		}
	}
	return "", fmt.Errorf("no unused first names remain for role %q", role)
}

// FirstName extracts the globally unique human part from a generated name.
func FirstName(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
