package settings

import (
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// MCPServer is an upstream MCP server omo relays, stored in
// mcpservers/<name>.yaml.
type MCPServer = api.MCPServer

// mcpServerPattern is the pattern of an MCP server name. omo names a relayed
// tool <server>__<tool>; a server name holds no "_", so the two parts never
// run into each other, and its length keeps the full tool name within the
// limits of the agent programs.
var mcpServerPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)

// mcpVarPattern is the pattern of an env or header name of an MCP server.
var mcpVarPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// MCPServerRoles are the agent roles an MCP server may be limited to.
var MCPServerRoles = []string{"ceo", "pm", "developer", "reviewer", "export"}

// Bounds of an MCP server's settings.
const (
	maxMCPDescription = 200 // characters
	maxMCPCommand     = 4096
	maxMCPArgs        = 256
	maxMCPVars        = 64 // env and headers, each
	maxMCPValue       = 32 << 10
	maxMCPToolNames   = 256 // allow and deny, each
	maxMCPToolName    = 128
	maxMCPScopes      = 64
	maxMCPScope       = 256
	maxMCPClientField = 1024 // OAuth client id and secret
	maxMCPSource      = 200
	minMCPTimeout     = time.Second
	maxMCPStartup     = 10 * time.Minute
	maxMCPCall        = 24 * time.Hour
)

func (s *Store) mcpServerDir() string { return filepath.Join(s.root, "mcpservers") }

func (s *Store) mcpServerPath(name string) string {
	return filepath.Join(s.mcpServerDir(), name+".yaml")
}

func (s *Store) mcpServerKind() *kind {
	return &kind{
		section: SectionMCPServers,
		dir:     s.mcpServerDir(),
		ext:     ".yaml",
		names:   mcpServerPattern,
		limit:   homefile.MaxSettingsFile,
		load: func(name string, data []byte) error {
			var m MCPServer
			if err := decodeYAML(data, &m); err != nil {
				return err
			}
			m.Name = name
			m = normalizeMCPServer(m)
			if err := validateMCPServer(m); err != nil {
				return err
			}
			s.mcp[name] = m
			return nil
		},
		drop: func(name string) bool {
			_, ok := s.mcp[name]
			delete(s.mcp, name)
			return ok
		},
	}
}

// remote reports whether a transport talks to a server over the network.
func remote(transport string) bool {
	return transport == api.MCPTransportHTTP || transport == api.MCPTransportSSE
}

// normalizeMCPServer copies the lists and maps (empty ones for none), fills
// what a file may leave out (instances per_session for stdio; auth headers
// for a remote server with headers, else none) and drops the revision.
func normalizeMCPServer(m MCPServer) MCPServer {
	m.Args = append([]string{}, m.Args...)
	m.Env = maps.Clone(m.Env)
	if m.Env == nil {
		m.Env = map[string]string{}
	}
	m.Headers = maps.Clone(m.Headers)
	if m.Headers == nil {
		m.Headers = map[string]string{}
	}
	m.HeadersCommand = append([]string{}, m.HeadersCommand...)
	m.OAuth.Scopes = append([]string{}, m.OAuth.Scopes...)
	m.Roles = append([]string{}, m.Roles...)
	m.Tools.Allow = append([]string{}, m.Tools.Allow...)
	m.Tools.Deny = append([]string{}, m.Tools.Deny...)
	if m.Transport == api.MCPTransportStdio && m.Instances == "" {
		m.Instances = api.MCPInstancesPerSession
	}
	if remote(m.Transport) && m.Auth == "" {
		m.Auth = api.MCPAuthNone
		if len(m.Headers) > 0 || len(m.HeadersCommand) > 0 {
			m.Auth = api.MCPAuthHeaders
		}
	}
	m.Revision = ""
	return m
}

// validateMCPServer checks a normalized MCP server. The error is a
// *FieldError naming the key; its message never quotes a value: env and
// header values, the OAuth client secret and often the address hold
// credentials.
func validateMCPServer(m MCPServer) error {
	if utf8.RuneCountInString(m.Description) > maxMCPDescription {
		return fieldErr("description", "description must be at most %d characters long", maxMCPDescription)
	}
	if hasHidden(m.Description) || !utf8.ValidString(m.Description) {
		return fieldErr("description", "description must be one line without control, invisible or direction characters")
	}
	switch m.Transport {
	case api.MCPTransportStdio:
		if err := validateMCPStdio(m); err != nil {
			return err
		}
	case api.MCPTransportHTTP, api.MCPTransportSSE:
		if err := validateMCPRemote(m); err != nil {
			return err
		}
	default:
		return fieldErr("transport", "transport must be stdio, http or sse")
	}
	switch m.Instances {
	case "", api.MCPInstancesPerSession, api.MCPInstancesShared:
	default:
		return fieldErr("instances", "instances must be per_session or shared")
	}
	if err := validateMCPVars("env", m.Env, false); err != nil {
		return err
	}
	if err := validateMCPVars("headers", m.Headers, true); err != nil {
		return err
	}
	if err := validateMCPList("args", m.Args, maxMCPArgs, maxMCPValue, true); err != nil {
		return err
	}
	if err := validateMCPList("headers_command", m.HeadersCommand, maxMCPArgs, maxMCPValue, true); err != nil {
		return err
	}
	if len(m.HeadersCommand) > 0 && strings.TrimSpace(m.HeadersCommand[0]) == "" {
		return fieldErr("headers_command.0", "headers_command.0 must name the program to run")
	}
	if err := validateMCPOAuth(m); err != nil {
		return err
	}
	for i, role := range m.Roles {
		key := fmt.Sprintf("roles.%d", i)
		if !slices.Contains(MCPServerRoles, role) {
			return fieldErr(key, "%s must be one of %s", key, strings.Join(MCPServerRoles, ", "))
		}
		if slices.Contains(m.Roles[:i], role) {
			return fieldErr(key, "%s names a role a second time", key)
		}
	}
	for _, l := range []struct {
		key   string
		names []string
	}{{"tools.allow", m.Tools.Allow}, {"tools.deny", m.Tools.Deny}} {
		if err := validateMCPList(l.key, l.names, maxMCPToolNames, maxMCPToolName, false); err != nil {
			return err
		}
	}
	for _, d := range []struct {
		key string
		v   Duration
		max time.Duration
	}{{"timeouts.startup", m.Timeouts.Startup, maxMCPStartup}, {"timeouts.call", m.Timeouts.Call, maxMCPCall}} {
		if v := time.Duration(d.v); v != 0 && (v < minMCPTimeout || v > d.max) {
			return fieldErr(d.key, "%s must be empty (omo's default) or between %s and %s", d.key, durationText(minMCPTimeout), durationText(d.max))
		}
	}
	if utf8.RuneCountInString(m.Source) > maxMCPSource || hasHidden(m.Source) || !utf8.ValidString(m.Source) {
		return fieldErr("source", "source must be at most %d characters without control, invisible or direction characters", maxMCPSource)
	}
	return nil
}

// validateMCPStdio checks the fields of a server omo starts as a process.
func validateMCPStdio(m MCPServer) error {
	if strings.TrimSpace(m.Command) == "" {
		return fieldErr("command", "command must name the program of a stdio server")
	}
	if len(m.Command) > maxMCPCommand || hasHidden(m.Command) || !utf8.ValidString(m.Command) {
		return fieldErr("command", "command must be at most %d characters without control, invisible or direction characters", maxMCPCommand)
	}
	if m.Cwd != "" && (!filepath.IsAbs(m.Cwd) || hasHidden(m.Cwd) || !utf8.ValidString(m.Cwd)) {
		return fieldErr("cwd", "cwd must be empty (the session's folder) or an absolute folder path")
	}
	if m.Auth != "" && m.Auth != api.MCPAuthNone {
		return fieldErr("auth", "auth applies to http and sse servers; a stdio server takes its credentials from env")
	}
	return nil
}

// validateMCPRemote checks the address and sign-in of an http or sse
// server.
func validateMCPRemote(m MCPServer) error {
	if m.URL == "" {
		return fieldErr("url", "url must be the address of an http or sse server")
	}
	if len(m.URL) > maxAddress || hasHidden(m.URL) || !utf8.ValidString(m.URL) {
		return fieldErr("url", "url must be at most %d characters without control, invisible or direction characters", maxAddress)
	}
	u, err := url.Parse(m.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		return fieldErr("url", "url must be an http:// or https:// address")
	}
	if u.Hostname() == "" {
		return fieldErr("url", "url must name a host")
	}
	if u.User != nil {
		return fieldErr("url", "url must not hold a user name or password; put credentials into headers")
	}
	if u.Fragment != "" || strings.Contains(m.URL, "#") {
		return fieldErr("url", "url must not hold a # part")
	}
	if m.Instances == api.MCPInstancesPerSession {
		return fieldErr("instances", "instances applies to stdio servers; omo keeps one connection to an http or sse server")
	}
	switch m.Auth {
	case api.MCPAuthNone, api.MCPAuthHeaders, api.MCPAuthOAuth:
	default:
		return fieldErr("auth", "auth must be none, headers or oauth")
	}
	return nil
}

// validateMCPOAuth checks the OAuth client of a server.
func validateMCPOAuth(m MCPServer) error {
	o := m.OAuth
	if m.Auth != api.MCPAuthOAuth {
		if o.ClientID != "" || o.ClientSecret != "" || len(o.Scopes) > 0 || o.Resource != "" {
			return fieldErr("oauth", "oauth applies only to an http or sse server with auth oauth")
		}
		return nil
	}
	for _, f := range []struct{ key, v string }{{"oauth.client_id", o.ClientID}, {"oauth.client_secret", o.ClientSecret}} {
		if len(f.v) > maxMCPClientField || hasHidden(f.v) || !utf8.ValidString(f.v) {
			return fieldErr(f.key, "%s must be at most %d characters without control, invisible or direction characters", f.key, maxMCPClientField)
		}
	}
	if o.ClientSecret != "" && o.ClientID == "" {
		return fieldErr("oauth.client_secret", "oauth.client_secret needs oauth.client_id: a client registered by discovery has no secret to enter")
	}
	if len(o.Scopes) > maxMCPScopes {
		return fieldErr("oauth.scopes", "oauth.scopes may hold at most %d scopes", maxMCPScopes)
	}
	for i, sc := range o.Scopes {
		key := fmt.Sprintf("oauth.scopes.%d", i)
		if sc == "" || len(sc) > maxMCPScope || strings.ContainsAny(sc, " \"\\") || hasHidden(sc) || !utf8.ValidString(sc) {
			return fieldErr(key, "%s must be one scope of at most %d characters without spaces, quotes or backslashes", key, maxMCPScope)
		}
	}
	if o.Resource != "" {
		u, err := url.Parse(o.Resource)
		if err != nil || len(o.Resource) > maxAddress || hasHidden(o.Resource) || (u.Scheme != "http" && u.Scheme != "https") ||
			u.Hostname() == "" || u.Fragment != "" || strings.Contains(o.Resource, "#") {
			return fieldErr("oauth.resource", "oauth.resource must be empty or an http:// or https:// address with a host and without a # part")
		}
	}
	return nil
}

// validateMCPVars checks the env or headers of a server: at most maxMCPVars
// names matching mcpVarPattern, each value at most maxMCPValue bytes without
// a NUL byte; a header value also without control, invisible or direction
// characters (a line break would start another header). Names may be shown;
// values never are.
func validateMCPVars(field string, vars map[string]string, header bool) error {
	if len(vars) > maxMCPVars {
		return fieldErr(field, "%s may hold at most %d names", field, maxMCPVars)
	}
	for _, name := range sortedKeys(vars) {
		if !mcpVarPattern.MatchString(name) {
			return fieldErr(field, "%s has an invalid name: every name must match %s", field, mcpVarPattern)
		}
		key := field + "." + name
		v := vars[name]
		if len(v) > maxMCPValue {
			return fieldErr(key, "the value of %s in %s is longer than %d KiB", name, field, maxMCPValue>>10)
		}
		if strings.ContainsRune(v, 0) || !utf8.ValidString(v) || (header && hasHidden(v)) {
			what := "a NUL byte"
			if header {
				what = "a line break or another control, invisible or direction character"
			}
			return fieldErr(key, "the value of %s in %s must be valid UTF-8 text without %s", name, field, what)
		}
	}
	return nil
}

// validateMCPList checks a list of strings: at most max entries of at most
// size bytes each, without hidden characters; with allowEmpty false no
// entry may be empty or repeat an earlier one.
func validateMCPList(field string, list []string, max, size int, allowEmpty bool) error {
	if len(list) > max {
		return fieldErr(field, "%s may hold at most %d entries", field, max)
	}
	for i, v := range list {
		key := fmt.Sprintf("%s.%d", field, i)
		switch {
		case len(v) > size:
			return fieldErr(key, "%s must be at most %d characters long", key, size)
		case hasHidden(v) || !utf8.ValidString(v):
			return fieldErr(key, "%s must not hold control, invisible or direction characters", key)
		case !allowEmpty && v == "":
			return fieldErr(key, "%s must not be empty", key)
		case !allowEmpty && slices.Contains(list[:i], v):
			return fieldErr(key, "%s names an entry a second time", key)
		}
	}
	return nil
}

// ValidateMCPServer checks m as PutMCPServerIf does, without writing: nil or
// an error with code validation naming the field.
func ValidateMCPServer(m MCPServer) error {
	if !mcpServerPattern.MatchString(m.Name) {
		return apiError(api.CodeValidation, fieldErr("name", "the MCP server name must match %s (at most 24 characters)", mcpServerPattern))
	}
	if err := validateMCPServer(normalizeMCPServer(m)); err != nil {
		return apiError(api.CodeValidation, err)
	}
	return nil
}

// MCPServers lists the MCP servers sorted by name, with their revisions.
func (s *Store) MCPServers() []MCPServer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MCPServer, 0, len(s.mcp))
	for name, m := range s.mcp {
		m = normalizeMCPServer(m)
		m.Revision = s.revisionLocked(s.mcpServerPath(name))
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MCPServer returns one MCP server by name, with its revision.
func (s *Store) MCPServer(name string) (MCPServer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.mcp[name]
	if !ok {
		return MCPServer{}, false
	}
	m = normalizeMCPServer(m)
	m.Revision = s.revisionLocked(s.mcpServerPath(name))
	return m, true
}

// PutMCPServerIf creates or replaces an MCP server and writes its file,
// guarded by a revision: the file is read again first, and when rev is not
// empty and differs from the file's revision the write is refused with code
// stale_settings; rev NoRevision means create only. Invalid servers are
// refused with code validation naming the field.
func (s *Store) PutMCPServerIf(rev string, m MCPServer) error {
	if err := ValidateMCPServer(m); err != nil {
		return err
	}
	m = normalizeMCPServer(m)
	data, err := encodeYAML(m)
	if err != nil {
		return api.Errorf(api.CodeInternal, "encode MCP server: %v", err)
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionMCPServers, m.Name, rev)
		if err != nil {
			return changes, err
		}
		if err := s.writeFile(s.mcpServerPath(m.Name), data); err != nil {
			return changes, err
		}
		s.mcp[m.Name] = m
		return append(changes, Change{Section: SectionMCPServers, Name: m.Name}), nil
	})
}

// DeleteMCPServerIf removes an MCP server's file, guarded by a revision as
// PutMCPServerIf. A file that could not be loaded can be deleted too; an
// unknown server gives not_found.
func (s *Store) DeleteMCPServerIf(rev, name string) error {
	if !mcpServerPattern.MatchString(name) {
		return api.Errorf(api.CodeNotFound, "no MCP server %s", showKey(name))
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionMCPServers, name, rev)
		if err != nil {
			return changes, err
		}
		_, loaded := s.mcp[name]
		path := s.mcpServerPath(name)
		if !loaded && !fileExists(path) {
			return changes, api.Errorf(api.CodeNotFound, "no MCP server %s", name)
		}
		if err := s.removeFile(path); err != nil {
			return changes, err
		}
		delete(s.mcp, name)
		return append(changes, Change{Section: SectionMCPServers, Name: name, Removed: true}), nil
	})
}
