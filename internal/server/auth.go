package server

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// hostOmoLocalhost is the host name of the printed access address. Browsers
// resolve every *.localhost name to loopback.
const hostOmoLocalhost = "omo.localhost"

const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; font-src 'self'; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// webSocketTokenPrefix marks the login token in Sec-WebSocket-Protocol;
// browsers cannot set Authorization on a websocket handshake.
const webSocketTokenPrefix = "omo-token."

func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", contentSecurityPolicy)
}

// isAPI reports whether path belongs to the JSON API.
func isAPI(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// BrowserHandler serves the routes to the browser over TCP: security
// headers, Host and Origin checks, authentication per the start options. The
// caller is always the user.
func (s *Server) BrowserHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		r = withRequestContext(r, board.User, s.log)
		if !s.allowedHost(r) || (s.opts.Config.OriginCheck && !sameOrigin(r)) {
			reject(w, r, api.Errorf(api.CodeForbidden, "host or origin rejected"))
			return
		}
		switch s.opts.Config.Auth {
		case "basic":
			if !s.basicAuthorized(r) {
				w.Header().Set("WWW-Authenticate", `Basic realm="omo", charset="UTF-8"`)
				reject(w, r, api.Errorf(api.CodeUnauthorized, "basic authentication required"))
				return
			}
		case "none":
		default:
			if isAPI(r.URL.Path) && !s.tokenAuthorized(r) {
				reject(w, r, api.Errorf(api.CodeUnauthorized, "the access URL's login token is required"))
				return
			}
		}
		s.mux.ServeHTTP(w, r)
	})
}

// reject answers with the error envelope on API paths and plain text
// elsewhere.
func reject(w http.ResponseWriter, r *http.Request, e *api.Error) {
	if isAPI(r.URL.Path) {
		writeError(w, r, e)
		return
	}
	http.Error(w, e.Message, e.Status)
}

func (s *Server) basicAuthorized(r *http.Request) bool {
	wantUser, wantPassword, _ := strings.Cut(s.opts.Config.BasicAuth, ":")
	user, password, ok := r.BasicAuth()
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(wantUser)) == 1
	passwordOK := subtle.ConstantTimeCompare([]byte(password), []byte(wantPassword)) == 1
	return ok && wantUser != "" && userOK && passwordOK
}

func (s *Server) tokenAuthorized(r *http.Request) bool {
	if s.token == "" {
		return false
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		for _, p := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
			if t, found := strings.CutPrefix(strings.TrimSpace(p), webSocketTokenPrefix); found {
				token, ok = t, true
			}
		}
	}
	return ok && subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}

// allowedHost checks the Host header against the address the connection
// arrived on. The port must match; the name must be omo.localhost,
// localhost or a loopback IP, or any IP literal when the server listens
// beyond loopback. Other names are refused, which defeats DNS rebinding.
func (s *Server) allowedHost(r *http.Request) bool {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		return false
	}
	boundPort, exposed := s.boundAddress(r)
	if port != boundPort {
		return false
	}
	if strings.EqualFold(host, hostOmoLocalhost) || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || exposed
}

// boundAddress returns the local port of the request's connection and
// whether the server listens beyond loopback.
func (s *Server) boundAddress(r *http.Request) (port string, exposed bool) {
	exposed = s.opts.Config.Host != "localhost" && !net.ParseIP(s.opts.Config.Host).IsLoopback()
	if addr, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr); ok {
		return strconv.Itoa(addr.Port), exposed
	}
	return strconv.Itoa(s.opts.Config.Port), exposed
}

// sameOrigin rejects cross-site requests: an Origin header must name
// exactly this server.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme && strings.EqualFold(u.Host, r.Host) && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}
