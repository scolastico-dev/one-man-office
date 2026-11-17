package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/mcpserver"
)

// mcpPath is omo's MCP endpoint, on the TCP listener and on the socket.
const mcpPath = "/mcp"

// sessionBearer is the form of an agent session id used as a bearer.
var sessionBearer = regexp.MustCompile(`^[0-9a-f]{32}$`)

// mcpTokenVerifier checks a bearer of an MCP client that is no agent
// session, a personal access token or an OAuth access token, for resource
// (<base>/mcp). It returns api.MCPTokenPAT or api.MCPTokenOAuth, or an
// error for a token it does not accept.
type mcpTokenVerifier func(ctx context.Context, token, resource string) (kind string, err error)

// refuseMCPTokens accepts no token: no personal access token or OAuth
// grant exists yet.
func refuseMCPTokens(context.Context, string, string) (string, error) {
	return "", api.Errorf(api.CodeUnauthorized, "unknown or expired token")
}

// mcpEndpoint is the state of omo's MCP endpoint.
type mcpEndpoint struct {
	once    sync.Once
	handler http.Handler
	// relay offers the relayed servers; nil offers none.
	relay mcpserver.Relay
	// verify checks personal and OAuth tokens; nil accepts none.
	verify mcpTokenVerifier

	mu sync.Mutex
	// seen are the agent sessions that used the endpoint.
	seen map[string]bool
}

// ownAuth reports whether path authenticates its callers itself, with
// bearer tokens, and is exempt from basic and login-token authentication
// and from the JSON content rule.
func ownAuth(path string) bool {
	return path == mcpPath
}

// mcpHandler returns the handler of /mcp, built at its first use.
func (s *Server) mcpHandler() http.Handler {
	s.mcp.once.Do(func() {
		opts := mcpserver.Options{
			Dispatch: func(id mcpserver.Identity) *apiclient.Client {
				return apiclient.NewWithTransport(apiclient.HandlerTransport(s.SocketHandler()), id.Session)
			},
			Seen:    s.mcpSeen,
			Log:     s.log,
			Version: s.opts.Version,
		}
		if s.mcp.relay != nil {
			opts.Relay = s.mcp.relay
		}
		s.mcp.handler = mcpserver.NewHandler(opts)
	})
	return s.mcp.handler
}

// serveMCP serves /mcp as the identity of the request's credential. On
// the socket the X-Omo-Session header names an agent session; on both
// listeners a bearer may: a session id, or a personal or OAuth token,
// which is tied to no session. A request with no credential is tied to no
// session on the socket and with auth none; elsewhere it is refused. An
// unknown or ended session is refused and never taken for anything else.
func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request, local bool) {
	id, err := s.mcpIdentity(r, local)
	if err != nil {
		var e *api.Error
		if errors.As(err, &e) && !local && (e.Code == api.CodeUnauthorized || e.Code == api.CodeUnknownSession) {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource%s", scope="mcp"`, s.requestBase(r), mcpPath))
		}
		writeError(w, r, err)
		return
	}
	if id.Class == mcpserver.ClassAgent {
		r = withRequestContext(r, id.Actor, s.log)
	}
	s.mcpHandler().ServeHTTP(w, r.WithContext(mcpserver.WithIdentity(r.Context(), id)))
}

func (s *Server) mcpIdentity(r *http.Request, local bool) (mcpserver.Identity, error) {
	ctx := r.Context()
	if a := actorFrom(r); local && a.Session != "" {
		// The socket handler resolved X-Omo-Session.
		return s.agentIdentity(ctx, a, "socket")
	}
	auth := r.Header.Get("Authorization")
	if auth == "" {
		switch {
		case local:
			return mcpserver.Identity{Class: mcpserver.ClassUser, Instance: "user", Local: true, Via: "socket"}, nil
		case s.opts.Config.Auth == "none":
			return mcpserver.Identity{Class: mcpserver.ClassUser, Instance: "user", Via: "none"}, nil
		}
		return mcpserver.Identity{}, api.Errorf(api.CodeUnauthorized, "a bearer token is required")
	}
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || token == "" {
		return mcpserver.Identity{}, api.Errorf(api.CodeUnauthorized, "the Authorization header must be a bearer token")
	}
	if sessionBearer.MatchString(token) {
		a, err := s.board.ResolveSession(ctx, token)
		if err != nil {
			return mcpserver.Identity{}, err
		}
		id, err := s.agentIdentity(ctx, a, "session")
		id.Local = local
		return id, err
	}
	verify := s.mcp.verify
	if verify == nil {
		verify = refuseMCPTokens
	}
	kind, err := verify(ctx, token, s.requestBase(r)+mcpPath)
	if err != nil {
		// Never the verifier's own words: they might quote the token.
		return mcpserver.Identity{}, api.Errorf(api.CodeUnauthorized, "unknown or expired token")
	}
	return mcpserver.Identity{Class: mcpserver.ClassUser, Instance: "user", Local: local, Via: kind}, nil
}

// agentIdentity is the identity of the live agent session a.
func (s *Server) agentIdentity(ctx context.Context, a board.Actor, via string) (mcpserver.Identity, error) {
	id := mcpserver.Identity{Class: mcpserver.ClassAgent, Actor: a, Session: a.Session, Instance: a.Session, Local: via == "socket", Via: via}
	live, err := s.board.AgentSessions(ctx, board.User, true)
	if err != nil {
		return mcpserver.Identity{}, err
	}
	for _, sess := range live {
		if sess.ID == a.Session {
			id.Workdir = sess.Workdir
		}
	}
	return id, nil
}

// requestBase is the scheme and host the request was sent to, after the
// Host check: https for a TLS connection and for the host of an https
// public_url, whose proxy ends TLS.
func (s *Server) requestBase(r *http.Request) string {
	scheme := "http"
	host, _, _, _ := splitHostHeader(r.Host)
	if p, ok := s.opts.Config.Public(); r.TLS != nil || ok && p.Scheme == "https" && strings.EqualFold(p.Host, host) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// mcpSeen records that an agent session used the endpoint.
func (s *Server) mcpSeen(session string) {
	s.mcp.mu.Lock()
	defer s.mcp.mu.Unlock()
	if s.mcp.seen == nil {
		s.mcp.seen = map[string]bool{}
	}
	s.mcp.seen[session] = true
}

// addMCPMarks marks the sessions that used the endpoint and forgets the
// ended ones.
func (s *Server) addMCPMarks(ss []api.AgentSession) {
	s.mcp.mu.Lock()
	defer s.mcp.mu.Unlock()
	for i := range ss {
		if !s.mcp.seen[ss[i].ID] {
			continue
		}
		if ss[i].State == api.SessionEnded {
			delete(s.mcp.seen, ss[i].ID)
			continue
		}
		ss[i].MCP = api.MCPAgentConnected
	}
}
