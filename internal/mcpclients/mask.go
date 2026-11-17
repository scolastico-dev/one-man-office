package mcpclients

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/secreturl"
)

// Masked replaces a value omo read from a program's file in a preview.
const Masked = secreturl.Mask

// schemePrefix keeps the scheme word of an Authorization value visible.
var schemePrefix = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]* `)

// maskValue hides the literal text of a value and keeps its ${VAR}
// references and a leading scheme word ("Bearer "), which say how the value
// is built without showing it.
func maskValue(v string) string {
	if v == "" {
		return ""
	}
	prefix := ""
	if m := schemePrefix.FindString(v); m != "" && len(m) < len(v) {
		prefix, v = m, v[len(m):]
	}
	var b strings.Builder
	b.WriteString(prefix)
	last := 0
	for _, loc := range refPattern.FindAllStringIndex(v, -1) {
		if loc[0] > last {
			b.WriteString(Masked)
		}
		b.WriteString(v[loc[0]:loc[1]])
		last = loc[1]
	}
	if last < len(v) {
		b.WriteString(Masked)
	}
	return b.String()
}

func maskMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = maskValue(v)
	}
	return out
}

// MaskServer returns s with its environment and header values, client
// secret and address secrets masked; the keys stay visible.
func MaskServer(s api.MCPServer) api.MCPServer {
	s.Env = maskMap(s.Env)
	s.Headers = maskMap(s.Headers)
	if s.OAuth.ClientSecret != "" {
		s.OAuth.ClientSecret = Masked
	}
	if s.URL != "" {
		s.URL = secreturl.Display(s.URL)
	}
	return s
}

// MaskState returns st as GET /api/mcp/clients shows it: every value masked.
func MaskState(st api.MCPClientState) api.MCPClientState {
	st.Entry = maskRaw(st.Entry)
	entries := make([]api.MCPClientEntry, len(st.Entries))
	for i, e := range st.Entries {
		e.Server = MaskServer(e.Server)
		entries[i] = e
	}
	st.Entries = entries
	return st
}

// MaskTakeover returns t as the preview shows it: every value masked.
func MaskTakeover(t api.MCPTakeover) api.MCPTakeover {
	out := api.MCPTakeover{Candidates: make([]api.MCPTakeoverCandidate, len(t.Candidates)), Stays: t.Stays}
	for i, c := range t.Candidates {
		c.Server = MaskServer(c.Server)
		out.Candidates[i] = c
	}
	return out
}

// secretKeys are the keys of a program's entry whose values are tables of
// secrets (secretTables) or secrets themselves (secretValues).
var (
	secretTables = []string{"env", "headers", "http_headers"}
	secretValues = []string{"bearer_token", "client_secret", "clientSecret"}
)

// maskRaw masks an entry of a program's file given as JSON.
func maskRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return json.RawMessage(`null`)
	}
	out, err := json.Marshal(maskAny(v))
	if err != nil {
		return json.RawMessage(`null`)
	}
	return out
}

func maskAny(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			switch {
			case slices.Contains(secretTables, k):
				if m, ok := x.(map[string]any); ok {
					masked := make(map[string]any, len(m))
					for mk, mv := range m {
						s, _ := mv.(string)
						if mk == "OMO_HOME" {
							// omo's own home in its bridge entry.
							masked[mk] = s
							continue
						}
						masked[mk] = maskValue(s)
					}
					out[k] = masked
					continue
				}
				out[k] = Masked
			case slices.Contains(secretValues, k):
				out[k] = Masked
			case k == "url" || k == "serverUrl" || k == "httpUrl":
				s, _ := x.(string)
				out[k] = secreturl.Display(s)
			default:
				out[k] = maskAny(x)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = maskAny(x)
		}
		return out
	}
	return v
}

// redactor masks known secret values in text: messages of failed commands
// and stores, which may echo what they read.
type redactor struct{ values []string }

// add records the literal parts of the server's secret values.
func (r *redactor) add(s api.MCPServer) {
	for _, v := range slices.Concat(slices.Collect(maps.Values(s.Env)), slices.Collect(maps.Values(s.Headers)), []string{s.OAuth.ClientSecret}) {
		for _, part := range refPattern.Split(v, -1) {
			part = strings.TrimSpace(schemePrefix.ReplaceAllString(part, ""))
			if len(part) >= 4 {
				r.values = append(r.values, part)
			}
		}
	}
	for _, v := range secreturl.Values(s.URL) {
		if len(v) >= 4 {
			r.values = append(r.values, v)
		}
	}
}

// addRaw records the secret values of a program's entries as read: values
// of the secret tables and keys, and passwords and query values of
// addresses, at any depth.
func (r *redactor) addRaw(v any) {
	addString := func(s string) {
		for _, part := range refPattern.Split(s, -1) {
			part = strings.TrimSpace(schemePrefix.ReplaceAllString(part, ""))
			if len(part) >= 4 {
				r.values = append(r.values, part)
			}
		}
	}
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			switch {
			case slices.Contains(secretTables, k):
				if m, ok := x.(map[string]any); ok {
					for _, mv := range m {
						s, _ := mv.(string)
						addString(s)
					}
				}
			case slices.Contains(secretValues, k):
				s, _ := x.(string)
				addString(s)
			case k == "url" || k == "serverUrl" || k == "httpUrl":
				s, _ := x.(string)
				r.add(api.MCPServer{URL: s})
			default:
				r.addRaw(x)
			}
		}
	case []any:
		for _, x := range v {
			r.addRaw(x)
		}
	}
}

func (r *redactor) redact(s string) string {
	if r == nil {
		return s
	}
	values := slices.Clone(r.values)
	slices.SortFunc(values, func(a, b string) int { return len(b) - len(a) })
	for _, v := range values {
		s = strings.ReplaceAll(s, v, Masked)
	}
	return s
}
