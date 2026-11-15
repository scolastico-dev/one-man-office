package settings

import (
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// secretWords mark a variable name as naming a secret, ignoring case. "AUTH"
// is handled apart: GIT_AUTHOR_NAME is no secret, AUTHORIZATION is.
var secretWords = []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL"}

// IsSecret reports whether a variable holds a secret: its name contains,
// ignoring case, KEY, TOKEN, SECRET, PASSWORD, PASSWD, CREDENTIAL or AUTH
// (but not as part of AUTHOR), and its value is literal and not empty. A value
// with a "$" (a reference to another variable) or a backtick (a command that
// prints the secret) is no secret itself.
func IsSecret(name, value string) bool {
	if value == "" || strings.ContainsAny(value, "$`") {
		return false
	}
	upper := strings.ToUpper(name)
	for _, w := range secretWords {
		if strings.Contains(upper, w) {
			return true
		}
	}
	for rest := upper; ; {
		i := strings.Index(rest, "AUTH")
		if i < 0 {
			return false
		}
		rest = rest[i+len("AUTH"):]
		if !strings.HasPrefix(rest, "OR") || strings.HasPrefix(rest, "ORIZ") || strings.HasPrefix(rest, "ORIS") {
			return true
		}
	}
}

// HideEnv returns a copy of env in which the value of every secret is
// api.HiddenValue. It is never nil.
func HideEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		if IsSecret(k, v) {
			v = api.HiddenValue
		}
		out[k] = v
	}
	return out
}

// RestoreEnv returns a copy of sent in which every api.HiddenValue is
// replaced by the stored value of the same variable, which must be a secret
// in stored. A hidden value without a stored secret (a new or renamed
// variable) is refused with a *FieldError naming field.NAME. It is never nil.
func RestoreEnv(sent, stored map[string]string, field string) (map[string]string, error) {
	out := make(map[string]string, len(sent))
	for _, k := range sortedKeys(sent) {
		v := sent[k]
		if v == api.HiddenValue {
			old, ok := stored[k]
			if !ok || !IsSecret(k, old) {
				return nil, fieldErr(field+"."+k, "Enter the value of %s again.", showKey(k))
			}
			v = old
		}
		out[k] = v
	}
	return out, nil
}
