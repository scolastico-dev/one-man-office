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
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

// hostOmoLocalhost is the host name of the printed access address. Browsers
// resolve every *.localhost name to loopback.
const hostOmoLocalhost = "omo.localhost"

// contentSecurityPolicy is sent with every answer. img-src allows blob: for
// uploaded images: the board fetches their bytes with its token and shows
// them through object URLs, so no token ever goes into an image address.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; font-src 'self'; img-src 'self' blob:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

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
		if !s.allowedHost(r) || (s.opts.Config.OriginCheck && !s.sameOrigin(r)) {
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
		if !requireJSON(w, r) {
			return
		}
		s.mux.ServeHTTP(s.withEventID(w, r), r)
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

// allowedHost checks the Host header. A Host without a port means port 80,
// as omo itself speaks plain HTTP. The built-in names (omo.localhost,
// localhost, loopback IPs) must name the port the connection arrived on; when
// the server listens beyond loopback, any IP literal qualifies under the same
// rule. The allowed_hosts option adds names the user trusts; the machine's
// own host name is not derived automatically, since an attacker may be able
// to make such a name resolve. Every other name is refused, which defeats DNS
// rebinding.
func (s *Server) allowedHost(r *http.Request) bool {
	host, port, hasPort, ok := splitHostHeader(r.Host)
	if !ok {
		return false
	}
	if !hasPort {
		port = "80"
	}
	boundPort, exposed := s.boundAddress(r)
	if port == boundPort && s.builtinHost(host, exposed) {
		return true
	}
	return s.listedHost(host, port, hasPort) || s.publicHost(host, port, hasPort)
}

// publicHost matches the host of the public_url option: exactly its name and
// its port (the scheme's default when the address names none, which is also
// what a Host header without a port stands for). No other name or port
// qualifies because of it.
func (s *Server) publicHost(host, port string, hasPort bool) bool {
	p, ok := s.opts.Config.Public()
	if !ok || !strings.EqualFold(p.Host, host) {
		return false
	}
	if !hasPort {
		port = p.DefaultPort()
	}
	return port == p.EffectivePort()
}

// builtinHost reports whether host is a name omo accepts without
// configuration.
func (s *Server) builtinHost(host string, exposed bool) bool {
	if strings.EqualFold(host, hostOmoLocalhost) || strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || exposed
	}
	return false
}

// listedHost matches host against the allowed_hosts entries. An entry with a
// port matches only a Host header carrying exactly that port; an entry
// without one matches the name on any port.
func (s *Server) listedHost(host, port string, hasPort bool) bool {
	for _, entry := range s.opts.Config.AllowedHosts {
		name, p, ok := serverconfig.SplitAllowedHost(entry)
		if !ok || !strings.EqualFold(name, host) {
			continue
		}
		if p == "" || (hasPort && p == port) {
			return true
		}
	}
	return false
}

// splitHostHeader splits a Host header into name and optional port. IPv6
// literals are returned without brackets.
func splitHostHeader(h string) (host, port string, hasPort, ok bool) {
	if h == "" {
		return "", "", false, false
	}
	if strings.HasPrefix(h, "[") {
		if !strings.Contains(h, "]:") {
			if !strings.HasSuffix(h, "]") {
				return "", "", false, false
			}
			host = h[1 : len(h)-1]
			return host, "", false, net.ParseIP(host) != nil
		}
		var err error
		if host, port, err = net.SplitHostPort(h); err != nil || net.ParseIP(host) == nil {
			return "", "", false, false
		}
		return host, port, true, validPort(port)
	}
	if !strings.Contains(h, ":") {
		return h, "", false, true
	}
	host, port, err := net.SplitHostPort(h)
	if err != nil || host == "" {
		return "", "", false, false
	}
	return host, port, true, validPort(port)
}

func validPort(p string) bool {
	n, err := strconv.Atoi(p)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == p
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

// sameOrigin rejects cross-site requests: an Origin header must name this
// server, that is the Host the request arrived with (an absent port means the
// scheme's default). Scheme http is accepted; https only when the connection
// is TLS or the host is an allowed_hosts entry, for a TLS-terminating proxy.
func (s *Server) sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || u.Opaque != "" {
		return false
	}
	defaultPort := "80"
	switch {
	case u.Scheme == "http" && r.TLS == nil:
	case u.Scheme == "https":
		defaultPort = "443"
	default:
		return false
	}
	oHost, oPort, oHasPort, ok := splitHostHeader(u.Host)
	if !ok {
		return false
	}
	rHost, rPort, rHasPort, ok := splitHostHeader(r.Host)
	if !ok || !strings.EqualFold(oHost, rHost) {
		return false
	}
	if !oHasPort {
		oPort = defaultPort
	}
	if !rHasPort {
		rPort = defaultPort
	}
	if oPort != rPort {
		return false
	}
	return u.Scheme == "http" || r.TLS != nil || s.listedHost(rHost, rPort, rHasPort) || s.publicOrigin(u.Scheme, rHost, rPort, rHasPort)
}

// publicOrigin accepts an https Origin for the host of an https public_url,
// the address of a TLS-terminating proxy in front of omo.
func (s *Server) publicOrigin(scheme, host, port string, hasPort bool) bool {
	p, ok := s.opts.Config.Public()
	return ok && scheme == "https" && p.Scheme == "https" && s.publicHost(host, port, hasPort)
}
