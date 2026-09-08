package session

import (
	"os"
	"sort"
	"strings"
)

// AddEnvironment appends configured agent variables after profile variables.
// Values support the shell-style ${NAME:fallback} and ${NAME:-fallback}
// forms used by the default Git identity settings, without invoking a shell.
func AddEnvironment(extra []string, configured map[string]string) []string {
	if len(configured) == 0 {
		return extra
	}
	values := make(map[string]string)
	for _, item := range append(os.Environ(), extra...) {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(configured))
	for key := range configured {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := expandConfiguredValue(configured[key], values)
		extra = append(extra, key+"="+value)
		values[key] = value
	}
	return extra
}

func expandConfiguredValue(value string, values map[string]string) string {
	for {
		start := strings.Index(value, "${")
		if start < 0 {
			break
		}
		end := strings.IndexByte(value[start+2:], '}')
		if end < 0 {
			break
		}
		end += start + 2
		expr := value[start+2 : end]
		name, fallback, hasFallback := strings.Cut(expr, ":")
		name = strings.TrimSuffix(name, "-")
		replacement := values[name]
		if replacement == "" && hasFallback {
			replacement = expandConfiguredValue(fallback, values)
		}
		value = value[:start] + replacement + value[end+1:]
	}
	for key, replacement := range values {
		value = strings.ReplaceAll(value, "$"+key, replacement)
	}
	return value
}

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
