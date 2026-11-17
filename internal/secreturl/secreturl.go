// Package secreturl handles addresses that may carry secrets: many hosted
// MCP servers take their key in the query (https://host/mcp?api_key=…),
// and an address may hold a password. Display is the one form of such an
// address omo shows, logs or records; Forms and Values are what a redactor
// learns so that the address, or its secret parts, never reach a log, an
// event, a state or an error as they are.
package secreturl

import (
	"maps"
	"net/url"
	"slices"
	"strings"
)

// Mask replaces a secret part of an address.
const Mask = "***"

// Display is raw as omo shows it: the password and every query value
// masked, the user name, host, path and query keys kept. A value that is
// not a URL is masked whole.
func Display(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return Mask
	}
	if u.RawQuery != "" {
		q := u.Query()
		keys := slices.Sorted(maps.Keys(q))
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = url.QueryEscape(k) + "=" + Mask
		}
		u.RawQuery = strings.Join(parts, "&")
	}
	out := u.String()
	if _, ok := u.User.Password(); ok {
		out = strings.Replace(out, u.User.String()+"@", url.User(u.User.Username()).String()+":"+Mask+"@", 1)
	}
	return out
}

// HasSecrets reports whether raw holds a password or a query value.
func HasSecrets(raw string) bool {
	return len(Values(raw)) > 0
}

// Forms are the ways raw appears in error texts: as written, as Go
// prints it again, and with the password replaced as Go's HTTP client
// does. Only an address with secrets has forms.
func Forms(raw string) []string {
	u, err := url.Parse(raw)
	if err != nil || !HasSecrets(raw) {
		return nil
	}
	return unique(raw, u.String(), u.Redacted())
}

// Values are the secret parts of raw (its password and query values), each
// as written, decoded and encoded, longest first.
func Values(raw string) []string {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	var out []string
	if p, ok := u.User.Password(); ok && p != "" {
		out = append(out, p, url.UserPassword("", p).String()[1:])
	}
	for part := range strings.SplitSeq(u.RawQuery, "&") {
		_, written, ok := strings.Cut(part, "=")
		if !ok || written == "" {
			continue
		}
		decoded, err := url.QueryUnescape(written)
		if err != nil {
			decoded = written
		}
		out = append(out, written, decoded, url.QueryEscape(decoded))
	}
	return unique(out...)
}

func unique(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	// Longest first, so a value that contains another is replaced whole.
	slices.SortStableFunc(out, func(a, b string) int { return len(b) - len(a) })
	return out
}
