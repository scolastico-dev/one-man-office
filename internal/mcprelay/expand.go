package mcprelay

import (
	"strings"
)

// expand replaces ${NAME} and ${NAME:-default} in s with values looked up
// in env (NAME=value entries; the last one wins). An unset NAME without a
// default becomes "". Everything else, a lone $ included, stays as written.
func expand(s string, env []string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		ref := s[i+2 : i+end]
		name, def, hasDefault := strings.Cut(ref, ":-")
		if !validName(name) {
			b.WriteString(s[i : i+end+1])
		} else if v, ok := lookupEnv(env, name); ok && (v != "" || !hasDefault) {
			b.WriteString(v)
		} else if hasDefault {
			b.WriteString(def)
		}
		s = s[i+end+1:]
	}
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func lookupEnv(env []string, name string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// childEnvironment is the environment of a stdio server's process: env
// without omo's own OMO_* variables, then the server's values expanded.
func childEnvironment(env []string, values map[string]string) []string {
	out := make([]string, 0, len(env)+len(values))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(k), "OMO_") {
			continue
		}
		if _, set := values[k]; set {
			continue
		}
		out = append(out, kv)
	}
	for _, k := range sortedKeys(values) {
		if strings.HasPrefix(strings.ToUpper(k), "OMO_") {
			continue
		}
		out = append(out, k+"="+expand(values[k], env))
	}
	return out
}
