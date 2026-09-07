package session

import (
	"os"
	"strings"
)

// Parent control credentials belong only to the office process. Agent CLI
// processes retain their socket identity without inheriting office credentials.
func processEnvironment(extra []string) []string {
	env := append(os.Environ(), extra...)
	clean := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if strings.EqualFold(key, "OMO_CONTROL_URL") || strings.EqualFold(key, "OMO_CONTROL_TOKEN") {
			continue
		}
		clean = append(clean, item)
	}
	return clean
}
