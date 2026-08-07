package names

import (
	"strings"
	"testing"
)

func TestPickFormatsAndPrefix(t *testing.T) {
	n, err := Pick("product_manager", func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(n, "pm-") {
		t.Fatalf("product_manager name %q must use pm- prefix", n)
	}
}

func TestPickAvoidsCollisions(t *testing.T) {
	seen := map[string]bool{}
	// Exhaust well past the raw list to force suffixing; every result unique.
	for i := 0; i < 200; i++ {
		n, err := Pick("developer", func(s string) bool { return seen[s] })
		if err != nil {
			t.Fatal(err)
		}
		if seen[n] {
			t.Fatalf("duplicate name %q", n)
		}
		seen[n] = true
	}
}

func TestPickUnknownRole(t *testing.T) {
	if _, err := Pick("janitor", func(string) bool { return false }); err == nil {
		t.Fatal("expected error for unknown role")
	}
}

func TestPickCanEnforceFirstNameUniquenessAcrossRoles(t *testing.T) {
	used := map[string]bool{}
	pick := func(role string) string {
		n, err := Pick(role, func(candidate string) bool { return used[FirstName(candidate)] })
		if err != nil {
			t.Fatal(err)
		}
		used[FirstName(n)] = true
		return n
	}
	a, b := pick("ceo"), pick("product_manager")
	if FirstName(a) == FirstName(b) {
		t.Fatalf("shared first name: %s and %s", a, b)
	}
}
