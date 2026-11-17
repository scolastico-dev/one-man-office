package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcpauth"
)

// mcpAuthFeature holds what authorizes MCP clients that are not omo's agent
// sessions, and omo's own sign-ins at upstream MCP servers.
type mcpAuthFeature struct {
	// tokens are the personal access tokens and OAuth grants; its Verify
	// checks the bearers of /mcp.
	tokens *mcpauth.Store
	// as is the OAuth authorization server of /mcp.
	as *mcpauth.AS
	// upstream keeps the relay's sign-ins at upstream servers.
	upstream *mcpauth.Upstream
	// public serves oauthPublicRoutes and nothing else.
	public *http.ServeMux
}

// oauthPublicRoutes are the endpoints of the OAuth authorization server
// outside /api. MCP clients call them before they hold any credential of
// omo, so they authenticate by their own means (PKCE, refresh token, signed
// client id) and are served without omo's basic credentials or login token
// (ownAuth). The consent page /oauth/authorize, the provider callback
// /oauth/callback and the consent API are not among them: they keep omo's
// protection.
var oauthPublicRoutes = []string{
	"GET " + mcpauth.PathResourceMetadata,
	"GET " + mcpauth.PathResourceMetadata + mcpauth.PathMCP,
	"GET " + mcpauth.PathServerMetadata,
	"POST " + mcpauth.PathToken,
	"POST " + mcpauth.PathRegister,
	"POST " + mcpauth.PathRevoke,
}

// oauthPublicPaths are the paths of oauthPublicRoutes.
var oauthPublicPaths = func() map[string]bool {
	out := map[string]bool{}
	for _, pattern := range oauthPublicRoutes {
		_, path, _ := strings.Cut(pattern, " ")
		out[path] = true
	}
	return out
}()

// registerMCPAuth serves the OAuth authorization server of /mcp: the two
// metadata documents, registration, token and revocation endpoints (outside
// /api, as the RFCs place them; they authenticate by their own means and are
// served by their own mux, see oauthPublicRoutes), and the consent API of
// the web app's /oauth/authorize page, for the user only. It hands the
// token store to /mcp, which verifies personal and OAuth tokens with it, and
// to the token routes of the settings.
func (s *Server) registerMCPAuth(mux *http.ServeMux) {
	tokens := mcpauth.NewStore(s.db, nil)
	s.mcpAuth = mcpAuthFeature{
		tokens:   tokens,
		as:       mcpauth.NewAS(tokens, nil, s.log),
		upstream: mcpauth.NewUpstream(s.db, nil),
		public:   http.NewServeMux(),
	}
	s.mcp.verify = func(ctx context.Context, token, resource string) (string, error) {
		g, err := tokens.Verify(ctx, token, resource)
		return g.Kind, err
	}
	s.mcpConf.tokens = tokens
	// The patterns are those of oauthPublicRoutes.
	public := s.mcpAuth.public
	public.HandleFunc("GET /.well-known/oauth-protected-resource", s.getMCPResourceMetadata)
	public.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.getMCPResourceMetadata)
	public.HandleFunc("GET /.well-known/oauth-authorization-server", s.getMCPServerMetadata)
	public.Handle("POST /oauth/token", s.mcpAuth.as.Token())
	public.Handle("POST /oauth/register", s.mcpAuth.as.Register())
	public.Handle("POST /oauth/revoke", s.mcpAuth.as.Revoke())
	s.registerRoutes(mux, []apiRoute{
		{"GET /api/oauth/consent", s.getOAuthConsent, userOnly},
		{"POST /api/oauth/consent", s.postOAuthConsent, userOnly},
	})
}

// mcpBase is the address a request reached omo by, without a trailing
// slash; it is the OAuth issuer and the base of the MCP resource. A request
// that came by the host of public_url gets public_url, with its scheme (so a
// TLS-terminating proxy in front of omo gives https); any other request
// gets its own scheme and Host, which BrowserHandler has already checked.
// Without public_url, an omo reached over plain http beyond loopback is
// therefore an http issuer such as http://192.168.1.5:8090.
func (s *Server) mcpBase(r *http.Request) string {
	if host, port, hasPort, ok := splitHostHeader(r.Host); ok && s.publicHost(host, port, hasPort) {
		if base := s.opts.Config.PublicBase(); base != "" {
			return strings.TrimSuffix(base, "/")
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(r.Host)
}

// mcpResource is the resource identifier of /mcp at the address r reached
// omo by: what an OAuth access token must be issued for to be accepted on
// r (mcpauth.Store.Verify's resource).
func (s *Server) mcpResource(r *http.Request) string {
	return s.mcpBase(r) + mcpauth.PathMCP
}

// mcpResources are the resources an authorization request on r may ask
// for: /mcp at r's own address, at public_url and at the loopback names on
// the bound port.
func (s *Server) mcpResources(r *http.Request) []string {
	out := []string{s.mcpResource(r)}
	if base := s.opts.Config.PublicBase(); base != "" {
		out = append(out, strings.TrimSuffix(base, "/")+mcpauth.PathMCP)
	}
	port, _ := s.boundAddress(r)
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]", hostOmoLocalhost} {
		out = append(out, "http://"+host+":"+port+mcpauth.PathMCP)
	}
	return out
}

func (s *Server) getMCPResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpauth.ProtectedResource(s.mcpBase(r)))
}

func (s *Server) getMCPServerMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpauth.AuthorizationServer(s.mcpBase(r)))
}

// getOAuthConsent checks an authorization request for the consent page; its
// query is the query of /oauth/authorize.
func (s *Server) getOAuthConsent(w http.ResponseWriter, r *http.Request) {
	c, err := s.mcpAuth.as.Consent(r.Context(), r.URL.Query(), s.mcpBase(r), s.mcpResources(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// postOAuthConsent applies the user's decision and answers where the
// browser goes next.
func (s *Server) postOAuthConsent(w http.ResponseWriter, r *http.Request) {
	var d api.OAuthDecision
	if err := decode(w, r, &d); err != nil {
		writeError(w, r, err)
		return
	}
	redirect, err := s.mcpAuth.as.Decide(r.Context(), d, s.mcpBase(r), s.mcpResources(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.OAuthRedirect{Redirect: redirect})
}
