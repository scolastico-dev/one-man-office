package childenvtest

import (
	_ "embed"
	"os"
	"strings"
	"testing"
)

//go:embed testdata/omo-variables.txt
var omoVariables string

// SetOmo poisons every known omo variable, plus future and mixed-case names.
// It is for environment guards only: no process should use these values.
func SetOmo(t testing.TB) {
	t.Helper()
	for _, key := range strings.Fields(omoVariables) {
		t.Setenv(key, "must-not-be-inherited")
	}
	t.Setenv("OMO_SOCKET", "/must-never-contact/omo.sock")
}

// CheckOmo allows only the test's own omo variables, with their own values.
// Report names only: an inherited value might be a real session credential.
func CheckOmo(t testing.TB, env []string, own map[string]string) {
	t.Helper()
	seen := map[string]bool{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "OMO_") {
			continue
		}
		want, ok := own[key]
		if !ok || value != want || seen[key] {
			t.Errorf("test environment leaks or duplicates %s", key)
		}
		seen[key] = true
	}
	for key := range own {
		if !seen[key] {
			t.Errorf("test environment lacks its own %s", key)
		}
	}
}

// ClearOmo removes inherited omo state in a test process, restoring it at cleanup.
func ClearOmo(t testing.TB) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "OMO_") {
			t.Setenv(key, "") // Register restoration before removing an inherited key.
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}
