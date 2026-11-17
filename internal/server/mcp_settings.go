package server

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// mcpRelay is what the MCP settings routes need from the relay of upstream
// MCP servers (internal/mcprelay). Errors that are *api.Error are answered
// as they are and must hold no value of a server's env, headers or OAuth
// client; any other error is neither answered nor logged.
type mcpRelay interface {
	// Status is the state of every relayed server.
	Status() []api.MCPServerState
	// Restart restarts a server, also one that failed.
	Restart(name string) error
	// Test starts a draft server, lists its tools and stops it.
	Test(ctx context.Context, m api.MCPServer) (api.MCPTestResult, error)
	// Connect starts the sign-in at a server with auth oauth and returns
	// the address the browser opens.
	Connect(ctx context.Context, name string) (authorizeURL string, err error)
	// Callback answers the provider's redirect to /oauth/callback.
	Callback(w http.ResponseWriter, r *http.Request)
	// Disconnect forgets omo's sign-in at a server.
	Disconnect(ctx context.Context, name string) error
	// Changed tells the relay that the settings of these servers changed
	// (written, edited by hand or removed).
	Changed(names []string)
	// LogLines are the last n lines of a server's log, oldest first, with
	// the values of its env and headers redacted.
	LogLines(name string, n int) []api.MCPLogLine
}

// mcpTokens is what the routes need from the store of the personal access
// tokens and OAuth grants of MCP clients (internal/mcpauth).
type mcpTokens interface {
	CreatePAT(ctx context.Context, name string, expires *time.Time) (api.MCPToken, string, error)
	List(ctx context.Context) ([]api.MCPToken, error)
	Revoke(ctx context.Context, id string) error
}

// mcpSettingsFeature holds what the MCP settings routes work with. A nil
// relay or token store answers the routes that need it with unavailable.
type mcpSettingsFeature struct {
	relay  mcpRelay
	tokens mcpTokens
}

// Bounds of the MCP settings routes.
const (
	mcpLogLines     = 200
	maxMCPTokenName = 100 // characters
)

// mcpTokenID is the form of a token or grant id.
var mcpTokenID = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

// registerMCPSettings registers the routes of the MCP settings. They are
// the user's only: they manage the commands omo runs for relayed servers,
// the credentials it holds for them and the tokens MCP clients reach omo
// with. The OAuth callback is the provider's redirect in the user's
// browser; it is no API route and takes no JSON.
func (s *Server) registerMCPSettings(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"GET /api/mcp/servers", s.listMCPServers, userOnly},
		{"GET /api/mcp/servers/{name}", s.getMCPServer, userOnly},
		{"PUT /api/mcp/servers/{name}", s.putMCPServer, userOnly},
		{"DELETE /api/mcp/servers/{name}", s.deleteMCPServer, userOnly},
		{"POST /api/mcp/servers/test", s.testMCPServer, userOnly},
		{"POST /api/mcp/servers/{name}/restart", s.restartMCPServer, userOnly},
		{"GET /api/mcp/servers/{name}/log", s.mcpServerLog, userOnly},
		{"POST /api/mcp/servers/{name}/connect", s.connectMCPServer, userOnly},
		{"POST /api/mcp/servers/{name}/disconnect", s.disconnectMCPServer, userOnly},
		{"GET /api/mcp/tokens", s.listMCPTokens, userOnly},
		{"POST /api/mcp/tokens", s.createMCPToken, userOnly},
		{"DELETE /api/mcp/tokens/{id}", s.revokeMCPToken, userOnly},
	})
	mux.HandleFunc("GET /oauth/callback", s.mcpOAuthCallback)
}

// mcpEmit records and announces an MCP event (mcp.changed with an
// api.MCPServerState, mcp.tools_changed without data). The relay gets it
// as its Emit.
func (s *Server) mcpEmit(kind string, data any) {
	if err := s.appendSystemEvent(kind, data); err != nil {
		s.log.Printf("record %s event: %v", kind, err)
		return
	}
	s.events.Wake()
}

// mcpSettingsChanged hands a change of an MCP server's file, also one made
// by hand, to the relay and announces that the relayed tools may have
// changed. A file that could not be loaded changes nothing: its last valid
// content stays in effect.
func (s *Server) mcpSettingsChanged(c settings.Change) {
	if c.Section != settings.SectionMCPServers || c.Err != nil || s.mcpConf.relay == nil {
		return
	}
	s.mcpConf.relay.Changed([]string{c.Name})
	s.mcpEmit(api.KindMCPToolsChanged, nil)
}

// mcpRelayOrRefuse returns the relay, or answers unavailable.
func (s *Server) mcpRelayOrRefuse(w http.ResponseWriter, r *http.Request) mcpRelay {
	if s.mcpConf.relay == nil {
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo's MCP relay is not available in this version"))
	}
	return s.mcpConf.relay
}

// mcpTokensOrRefuse returns the token store, or answers unavailable.
func (s *Server) mcpTokensOrRefuse(w http.ResponseWriter, r *http.Request) mcpTokens {
	if s.mcpConf.tokens == nil {
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo's MCP tokens are not available in this version"))
	}
	return s.mcpConf.tokens
}

// mcpRelayFailed answers an error of the relay. An *api.Error is answered
// as it is. Any other error may quote a value of the server's settings (a
// header, an address with a password): its text is neither answered nor
// logged; the server's own log, which redacts the values, tells more.
func mcpRelayFailed(w http.ResponseWriter, r *http.Request, what, name string, err error) {
	var e *api.Error
	if errors.As(err, &e) {
		writeError(w, r, e)
		return
	}
	logf(r, "%s %s: %s of MCP server %s failed (the reason is in the server's log)", r.Method, r.URL.Path, what, name)
	writeError(w, r, api.Errorf(api.CodeInternal, "the %s of MCP server %s failed; its log on the MCP page tells more", what, name))
}

// knownMCPServer returns the stored server of the path's name, or answers
// not_found.
func (s *Server) knownMCPServer(w http.ResponseWriter, r *http.Request) (api.MCPServer, bool) {
	name := r.PathValue("name")
	m, ok := s.settings.MCPServer(name)
	if !ok {
		writeError(w, r, api.Errorf(api.CodeNotFound, "no MCP server %q", name))
	}
	return m, ok
}

// listMCPServers answers the servers as stored (values included, as the
// profile and skill set pages show theirs), their states and the files
// that could not be loaded.
func (s *Server) listMCPServers(w http.ResponseWriter, r *http.Request) {
	errs := []api.FileError{}
	for _, e := range s.settings.Errors() {
		if filepath.Base(filepath.Dir(e.File)) == "mcpservers" {
			errs = append(errs, e)
		}
	}
	states := []api.MCPServerState{}
	if s.mcpConf.relay != nil {
		if st := s.mcpConf.relay.Status(); st != nil {
			states = st
		}
	}
	writeJSON(w, http.StatusOK, api.MCPServersResponse{Servers: s.settings.MCPServers(), States: states, Errors: errs})
}

func (s *Server) getMCPServer(w http.ResponseWriter, r *http.Request) {
	if m, ok := s.knownMCPServer(w, r); ok {
		writeJSON(w, http.StatusOK, m)
	}
}

// putMCPServer creates or replaces a server, guarded by If-Match ("0"
// creates only). The relay follows through the settings feed before the
// answer is sent.
func (s *Server) putMCPServer(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var m api.MCPServer
	if err := decodeSettings(w, r, &m); err != nil {
		writeError(w, r, err)
		return
	}
	name := r.PathValue("name")
	if err := bodyNameMatches(m.Name, name); err != nil {
		writeError(w, r, err)
		return
	}
	m.Name, m.Revision = name, ""
	if err := s.settings.PutMCPServerIf(rev, m); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	out, ok := s.settings.MCPServer(name)
	if !ok {
		writeError(w, r, api.Errorf(api.CodeConflict, "MCP server %s was removed right after it was written", name))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteMCPServer removes a server's file, guarded by If-Match; the relay
// stops it through the settings feed.
func (s *Server) deleteMCPServer(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.settings.DeleteMCPServerIf(rev, r.PathValue("name")); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// testMCPServer checks a draft as a write would, then lets the relay start
// it, list its tools and stop it. Nothing is saved; a draft without a name
// is tested as "draft".
func (s *Server) testMCPServer(w http.ResponseWriter, r *http.Request) {
	var m api.MCPServer
	if err := decodeSettings(w, r, &m); err != nil {
		writeError(w, r, err)
		return
	}
	if m.Name == "" {
		m.Name = "draft"
	}
	m.Revision = ""
	if err := settings.ValidateMCPServer(m); err != nil {
		writeError(w, r, err)
		return
	}
	relay := s.mcpRelayOrRefuse(w, r)
	if relay == nil {
		return
	}
	res, err := relay.Test(r.Context(), settings.NormalizeMCPServer(m))
	if err != nil {
		mcpRelayFailed(w, r, "test", m.Name, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) restartMCPServer(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if err := decodeSettings(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, ok := s.knownMCPServer(w, r)
	if !ok {
		return
	}
	relay := s.mcpRelayOrRefuse(w, r)
	if relay == nil {
		return
	}
	if err := relay.Restart(m.Name); err != nil {
		mcpRelayFailed(w, r, "restart", m.Name, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mcpServerLog(w http.ResponseWriter, r *http.Request) {
	m, ok := s.knownMCPServer(w, r)
	if !ok {
		return
	}
	relay := s.mcpRelayOrRefuse(w, r)
	if relay == nil {
		return
	}
	lines := relay.LogLines(m.Name, mcpLogLines)
	if lines == nil {
		lines = []api.MCPLogLine{}
	}
	writeJSON(w, http.StatusOK, lines)
}

// connectMCPServer starts omo's sign-in at a server with auth oauth and
// answers the address the browser opens; the provider sends the browser
// back to /oauth/callback.
func (s *Server) connectMCPServer(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if err := decodeSettings(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, ok := s.knownMCPServer(w, r)
	if !ok {
		return
	}
	if m.Auth != api.MCPAuthOAuth {
		writeError(w, r, api.Errorf(api.CodeConflict, "MCP server %s does not sign in with OAuth (auth is not oauth)", m.Name))
		return
	}
	relay := s.mcpRelayOrRefuse(w, r)
	if relay == nil {
		return
	}
	u, err := relay.Connect(r.Context(), m.Name)
	if err != nil {
		mcpRelayFailed(w, r, "sign-in", m.Name, err)
		return
	}
	writeJSON(w, http.StatusOK, api.MCPConnect{AuthorizeURL: u})
}

func (s *Server) disconnectMCPServer(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if err := decodeSettings(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, ok := s.knownMCPServer(w, r)
	if !ok {
		return
	}
	relay := s.mcpRelayOrRefuse(w, r)
	if relay == nil {
		return
	}
	if err := relay.Disconnect(r.Context(), m.Name); err != nil {
		mcpRelayFailed(w, r, "sign-out", m.Name, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mcpOAuthCallback hands the provider's redirect to the relay. Only the
// user's browser comes here; an agent session on the socket is refused.
func (s *Server) mcpOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	if s.mcpConf.relay == nil {
		http.Error(w, "omo's MCP relay is not available in this version.", http.StatusServiceUnavailable)
		return
	}
	s.mcpConf.relay.Callback(w, r)
}

func (s *Server) listMCPTokens(w http.ResponseWriter, r *http.Request) {
	store := s.mcpTokensOrRefuse(w, r)
	if store == nil {
		return
	}
	list, err := store.List(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	if list == nil {
		list = []api.MCPToken{}
	}
	writeJSON(w, http.StatusOK, list)
}

// createMCPToken creates a personal access token; its secret is in this
// answer only.
func (s *Server) createMCPToken(w http.ResponseWriter, r *http.Request) {
	var req api.MCPTokenRequest
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	switch {
	case name == "":
		writeError(w, r, mcpFieldError("name", "name must not be empty"))
		return
	case utf8.RuneCountInString(name) > maxMCPTokenName:
		writeError(w, r, mcpFieldError("name", "name must be at most 100 characters long"))
		return
	case strings.ContainsFunc(name, textsafe.IsHidden) || !utf8.ValidString(name):
		writeError(w, r, mcpFieldError("name", "name must not hold control, invisible or direction characters"))
		return
	case req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()):
		writeError(w, r, mcpFieldError("expires_at", "expires_at must lie in the future"))
		return
	}
	store := s.mcpTokensOrRefuse(w, r)
	if store == nil {
		return
	}
	tok, secret, err := store.CreatePAT(r.Context(), name, req.ExpiresAt)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.MCPTokenCreated{Token: tok, Secret: secret})
}

// revokeMCPToken revokes a personal access token or an OAuth grant.
func (s *Server) revokeMCPToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !mcpTokenID.MatchString(id) {
		writeError(w, r, api.Errorf(api.CodeNotFound, "no MCP token %q", id))
		return
	}
	store := s.mcpTokensOrRefuse(w, r)
	if store == nil {
		return
	}
	if err := store.Revoke(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mcpFieldError is a validation refusal naming field.
func mcpFieldError(field, msg string) *api.Error {
	e := api.Errorf(api.CodeValidation, "%s", msg)
	e.Details = api.FieldDetail{Field: field}
	return e
}
