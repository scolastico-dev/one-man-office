package mcpclients

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Notes shown with a taken-over server.
const (
	noteGlobal     = "available without an omo session too (global), as it was in the program"
	noteNeedsLogin = "needs login: click Connect in omo after the take-over; the program's own sign-in is not moved"
	noteGuessOAuth = "the program's entry has no headers; omo assumes the server signs in with OAuth (set authorization to none if it needs no sign-in)"
	noteShell      = "the headers helper runs through sh -c"
)

// refPattern finds ${VAR} and ${VAR:-default} references.
var refPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

// mapEntry maps the entry name of cli's file to omo's server settings. The
// reason is set when omo cannot take the entry over; notes say what the user
// should know.
func (c *Clients) mapEntry(cli, name string, v any) api.MCPClientEntry {
	e := api.MCPClientEntry{Name: name, Notes: []string{}}
	if strings.HasPrefix(name, "-") {
		e.Reason = "the name starts with '-'; remove or rename it in the program by hand"
		return e
	}
	raw, ok := v.(map[string]any)
	if !ok {
		e.Reason = "the entry is not a table of settings"
		return e
	}
	var s api.MCPServer
	var notes []string
	var reason string
	switch cli {
	case api.CLIClaude:
		s, notes, reason = mapClaude(raw)
	case api.CLICodex:
		s, notes, reason = mapCodex(raw)
	case api.CLIAgy:
		s, notes, reason = mapAgy(raw)
	}
	if reason != "" {
		e.Reason = reason
		return e
	}
	s.Name = name
	s.Global = true
	if s.Transport == api.MCPTransportStdio {
		s.Instances = api.MCPInstancesPerSession
	} else if s.Auth == "" {
		switch {
		case len(s.Headers) > 0 || len(s.HeadersCommand) > 0:
			s.Auth = api.MCPAuthHeaders
		default:
			s.Auth = api.MCPAuthOAuth
			notes = append(notes, noteGuessOAuth)
		}
	}
	notes = append(notes, noteGlobal)
	if s.Auth == api.MCPAuthOAuth {
		notes = append(notes, noteNeedsLogin)
	}
	notes = append(notes, c.variableNotes(s)...)
	e.Server = s
	e.Notes = notes
	return e
}

// variableNotes says for every ${VAR} in the server's environment and
// headers whether omo's environment has it, and warns about references omo
// does not expand.
func (c *Clients) variableNotes(s api.MCPServer) []string {
	seen := map[string]bool{}
	var out []string
	check := func(values map[string]string) {
		for _, k := range sortedKeys(values) {
			for _, m := range refPattern.FindAllStringSubmatch(values[k], -1) {
				name := m[1]
				if seen[name] {
					continue
				}
				seen[name] = true
				if _, ok := c.o.Getenv(name); ok {
					out = append(out, fmt.Sprintf("%s is set in omo's environment", name))
				} else {
					out = append(out, fmt.Sprintf("%s is not set in omo's environment; enter the value in the server's settings", name))
				}
			}
		}
	}
	check(s.Env)
	check(s.Headers)
	other := append([]string{s.Command, s.URL, s.Cwd}, s.Args...)
	if slices.ContainsFunc(other, func(x string) bool { return refPattern.MatchString(x) }) {
		out = append(out, "omo expands ${...} only in the environment and the headers, not in the command, arguments, folder or address")
	}
	return out
}

// stdio fills the stdio fields from command, args, env and cwd.
func stdio(raw map[string]any) (api.MCPServer, string) {
	s := api.MCPServer{Enabled: true, Transport: api.MCPTransportStdio, Command: str(raw["command"]), Cwd: str(raw["cwd"])}
	if s.Command == "" {
		return s, "the entry has no command"
	}
	args, ok := strs(raw["args"])
	if !ok {
		return s, "its arguments are not a list of strings"
	}
	s.Args = args
	env, ok := strMap(raw["env"])
	if !ok {
		return s, "its environment is not a table of strings"
	}
	s.Env = env
	return s, ""
}

// remote fills the http or sse fields from url and headers.
func remote(transport, url string, headers any) (api.MCPServer, string) {
	s := api.MCPServer{Enabled: true, Transport: transport, URL: url}
	h, ok := strMap(headers)
	if !ok {
		return s, "its headers are not a table of strings"
	}
	s.Headers = h
	return s, ""
}

func mapClaude(raw map[string]any) (api.MCPServer, []string, string) {
	var notes []string
	typ := str(raw["type"])
	if typ == "" {
		if raw["command"] != nil {
			typ = "stdio"
		} else {
			typ = "http"
		}
	}
	var s api.MCPServer
	var reason string
	switch typ {
	case "stdio":
		s, reason = stdio(raw)
	case "http", "streamable-http", "sse":
		t := api.MCPTransportHTTP
		if typ == "sse" {
			t = api.MCPTransportSSE
		}
		if str(raw["url"]) == "" {
			return s, nil, "the entry has no address"
		}
		s, reason = remote(t, str(raw["url"]), raw["headers"])
		if helper := str(raw["headersHelper"]); helper != "" {
			s.HeadersCommand = []string{"sh", "-c", helper}
			notes = append(notes, noteShell)
		}
		if o, ok := raw["oauth"].(map[string]any); ok {
			s.Auth = api.MCPAuthOAuth
			s.OAuth = api.MCPOAuth{ClientID: str(o["clientId"]), ClientSecret: str(o["clientSecret"]), Scopes: scopeList(o["scopes"])}
		}
	case "ws":
		return s, nil, "it uses the WebSocket transport, which omo does not relay"
	default:
		return s, nil, fmt.Sprintf("its transport %q is unknown to omo", clip(typ))
	}
	return s, notes, reason
}

func mapCodex(raw map[string]any) (api.MCPServer, []string, string) {
	var notes []string
	var s api.MCPServer
	var reason string
	switch {
	case str(raw["url"]) != "":
		s, reason = remote(api.MCPTransportHTTP, str(raw["url"]), raw["http_headers"])
		if reason != "" {
			return s, nil, reason
		}
		add := func(k, v string) {
			if s.Headers == nil {
				s.Headers = map[string]string{}
			}
			if _, ok := s.Headers[k]; !ok {
				s.Headers[k] = v
			}
		}
		if tok := str(raw["bearer_token"]); tok != "" {
			add("Authorization", "Bearer "+tok)
		}
		if v := str(raw["bearer_token_env_var"]); v != "" {
			add("Authorization", "Bearer ${"+v+"}")
		}
		envHeaders, ok := strMap(raw["env_http_headers"])
		if !ok {
			return s, nil, "its env_http_headers are not a table of strings"
		}
		for _, h := range sortedKeys(envHeaders) {
			add(h, "${"+envHeaders[h]+"}")
		}
		switch helper := raw["http_headers_helper"].(type) {
		case string:
			if helper != "" {
				s.HeadersCommand = []string{"sh", "-c", helper}
				notes = append(notes, noteShell)
			}
		case []any:
			argv, ok := strs(helper)
			if !ok {
				return s, nil, "its http_headers_helper is not a command"
			}
			s.HeadersCommand = argv
		case map[string]any:
			argv, _ := strs(helper["args"])
			if cmd := str(helper["command"]); cmd != "" {
				s.HeadersCommand = append([]string{cmd}, argv...)
			}
		}
		o, hasOAuth := raw["oauth"].(map[string]any)
		if hasOAuth || str(raw["auth"]) == "oauth" || raw["scopes"] != nil || str(raw["oauth_resource"]) != "" {
			s.Auth = api.MCPAuthOAuth
			s.OAuth = api.MCPOAuth{ClientID: str(o["client_id"]), ClientSecret: str(o["client_secret"]),
				Scopes: scopeList(o["scopes"]), Resource: str(raw["oauth_resource"])}
			if len(s.OAuth.Scopes) == 0 {
				s.OAuth.Scopes = scopeList(raw["scopes"])
			}
			if r := str(o["resource"]); r != "" && s.OAuth.Resource == "" {
				s.OAuth.Resource = r
			}
		}
	case str(raw["command"]) != "":
		s, reason = stdio(raw)
		if reason != "" {
			return s, nil, reason
		}
		vars, ok := raw["env_vars"].([]any)
		if !ok && raw["env_vars"] != nil {
			return s, nil, "its env_vars are not a list"
		}
		for _, v := range vars {
			name := str(v)
			if m, ok := v.(map[string]any); ok {
				name = str(m["name"])
			}
			if name == "" {
				continue
			}
			if s.Env == nil {
				s.Env = map[string]string{}
			}
			if _, ok := s.Env[name]; !ok {
				s.Env[name] = "${" + name + "}"
			}
		}
	default:
		return s, nil, "the entry has neither a command nor an address"
	}
	if b, ok := raw["enabled"].(bool); ok && !b {
		s.Enabled = false
	}
	allow, ok1 := strs(raw["enabled_tools"])
	deny, ok2 := strs(raw["disabled_tools"])
	if !ok1 || !ok2 {
		return s, nil, "its tool lists are not lists of names"
	}
	s.Tools = api.MCPToolFilter{Allow: allow, Deny: deny}
	if d, ok := seconds(raw["startup_timeout_sec"]); ok {
		s.Timeouts.Startup = d
	} else if ms, ok := seconds(raw["startup_timeout_ms"]); ok {
		s.Timeouts.Startup = ms / 1000
	}
	if d, ok := seconds(raw["tool_timeout_sec"]); ok {
		s.Timeouts.Call = d
	}
	if b, _ := raw["required"].(bool); b {
		notes = append(notes, "codex treats it as required; omo has no such switch")
	}
	return s, notes, ""
}

func mapAgy(raw map[string]any) (api.MCPServer, []string, string) {
	if str(raw["authProviderType"]) != "" {
		return api.MCPServer{}, nil, "it signs in with Google credentials (authProviderType), which omo cannot take over"
	}
	var s api.MCPServer
	var reason string
	url := str(raw["serverUrl"])
	if url == "" {
		url = str(raw["url"])
	}
	if url == "" {
		url = str(raw["httpUrl"])
	}
	switch {
	case url != "":
		s, reason = remote(api.MCPTransportHTTP, url, raw["headers"])
		if o, ok := raw["oauth"].(map[string]any); ok {
			s.Auth = api.MCPAuthOAuth
			s.OAuth = api.MCPOAuth{ClientID: str(o["clientId"]), ClientSecret: str(o["clientSecret"]), Scopes: scopeList(o["scopes"])}
		}
	case str(raw["command"]) != "":
		s, reason = stdio(raw)
	default:
		return s, nil, "the entry has neither a command nor an address"
	}
	if reason != "" {
		return s, nil, reason
	}
	if b, _ := raw["disabled"].(bool); b {
		s.Enabled = false
	}
	allow, ok1 := strs(raw["enabledTools"])
	deny, ok2 := strs(raw["disabledTools"])
	if !ok1 || !ok2 {
		return s, nil, "its tool lists are not lists of names"
	}
	s.Tools = api.MCPToolFilter{Allow: allow, Deny: deny}
	if d, ok := seconds(raw["timeoutSeconds"]); ok {
		s.Timeouts.Call = d
	}
	return s, nil, ""
}

// clip cuts a value read from a file for a message.
func clip(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}
