package server

import (
	"context"
	_ "embed"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/proxy"
)

const proxyInternalPrefix = "/_omo/proxy/"

//go:embed proxy_bootstrap.js
var proxyBootstrapScript []byte

type proxyOpenResult struct {
	URL string `json:"url"`
}

// proxyOrigin derives the browser origin from the accepted Host and deployment
// settings. Forwarded headers cannot change the scheme, port or grant binding.
func (s *Server) proxyOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p, ok := s.opts.Config.Public(); ok {
		host, port, hasPort, valid := splitHostHeader(r.Host)
		if valid && (s.publicHost(host, port, hasPort) || s.proxyPublicHost(host, port, hasPort)) {
			scheme = p.Scheme
		}
	}
	return canonicalProxyOrigin(scheme + "://" + r.Host)
}

// Browsers canonicalize host case and omit the default port from Host. Use
// that same origin spelling both when issuing and when consuming a grant.
func canonicalProxyOrigin(raw string) string {
	u, _ := url.Parse(raw)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		u.Host = host
	}
	return u.Scheme + "://" + u.Host
}

func (s *Server) proxyBoardURL(r *http.Request) string {
	if address := s.opts.Config.PublicBase(); address != "" {
		return canonicalProxyOrigin(address)
	}
	host, port, hasPort, _ := splitHostHeader(r.Host)
	if id, _ := s.proxyHost(host); id != "" {
		origin, _ := url.Parse(s.proxyOrigin(r))
		origin.Host = strings.ToLower(s.opts.Config.ProxyBaseHost)
		if hasPort {
			origin.Host = origin.Host + ":" + port
		}
		return canonicalProxyOrigin(origin.String())
	}
	return s.proxyOrigin(r)
}

// proxyOpenDestination is the extension point for subdomain Open routing.
// The record's start path was validated by the board; exchange clients never
// supply either the destination or a return path.
func (s *Server) proxyOpenDestination(r *http.Request, f api.ProxyForward) proxyDestination {
	origin := s.proxyBoardURL(r)
	if s.opts.Config.ProxyBaseHost != "" {
		origin = s.proxySubdomainOrigin(origin, f.ID)
		return proxyDestination{ForwardID: f.ID, Origin: origin, URL: origin + f.StartPath}
	}
	return proxyDestination{ForwardID: f.ID, Origin: origin, URL: origin + f.BasePath + strings.TrimPrefix(f.StartPath, "/")}
}

func (s *Server) openProxyForward(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeEmpty(w, r); err != nil {
			writeError(w, r, err)
			return
		}
	}
	f, err := s.board.LiveProxyForward(r.Context(), board.System, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	dest := s.proxyOpenDestination(r, f)
	if s.opts.Config.Auth == "basic" || s.opts.Config.Auth == "none" {
		s.setProxyCookie(w, r, proxy.ForwardCookie, f.ID)
		writeJSON(w, 200, proxyOpenResult{dest.URL})
		return
	}
	grant, err := s.proxyAuth.issue(dest, time.Now())
	if err != nil {
		writeError(w, r, api.Errorf(api.CodeUnavailable, "too many pending proxy opens; try again shortly"))
		return
	}
	// When the board is outside the shared cookie domain, its authenticated
	// Open response establishes a host-local session for the permanent path URL.
	if s.opts.Config.ProxyBaseHost != "" && s.proxyCookieDomain(r) == "" {
		if !s.proxyAuth.authorized(singleProxyCookie(r, proxy.SessionCookie), time.Now()) {
			session, err := s.proxyAuth.newSession(time.Now())
			if err != nil {
				s.proxyAuth.revokeGrant(grant)
				writeError(w, r, api.Errorf(api.CodeUnavailable, "too many active Web UI sessions; try again later"))
				return
			}
			s.setProxyCookie(w, r, proxy.SessionCookie, session)
		}
		s.setProxyCookie(w, r, proxy.ForwardCookie, f.ID)
	}
	writeJSON(w, 200, proxyOpenResult{dest.Origin + proxyInternalPrefix + "bootstrap#" + grant})
}

func (s *Server) setProxyCookie(w http.ResponseWriter, r *http.Request, name, value string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Domain: s.proxyCookieDomain(r), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(s.proxyOrigin(r), "https://"), MaxAge: int(proxySessionTTL.Seconds())})
}

func (s *Server) proxyPage(w http.ResponseWriter, r *http.Request, status int, message string) {
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><title>omo Web UI</title><p>%s</p><a id="board" href="%s/">Back to board</a></html>`, html.EscapeString(message), html.EscapeString(s.proxyBoardURL(r)))
}

func (s *Server) serveProxyInternal(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	// Open may navigate a reserved tab from a board outside the proxy site.
	// This document only loads the same-origin bootstrap script: it consumes no
	// grant and sets no cookies. Keep script/exchange and all other requests on
	// the normal Origin gate, including navigations that supply an Origin.
	bootstrapNavigation := r.URL.EscapedPath() == proxyInternalPrefix+"bootstrap" &&
		(r.Method == "GET" || r.Method == "HEAD") && len(r.Header.Values("Origin")) == 0 &&
		r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
	if s.opts.Config.OriginCheck && !bootstrapNavigation && !s.sameProxyInternalOrigin(r) {
		reject(w, r, api.Errorf(api.CodeForbidden, "host or origin rejected"))
		return
	}
	switch r.URL.EscapedPath() {
	case proxyInternalPrefix + "bootstrap":
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == "GET" {
			fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="referrer" content="no-referrer"><title>Opening Web UI</title><script src="/_omo/proxy/bootstrap.js" defer></script><p id="status">Opening Web UI…</p><a id="board" href="%s/">Back to board</a></html>`, html.EscapeString(s.proxyBoardURL(r)))
		}
	case proxyInternalPrefix + "bootstrap.js":
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		if r.Method == "GET" {
			w.Write(proxyBootstrapScript)
		}
	case proxyInternalPrefix + "exchange":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		if err := checkJSONContentType(r); err != nil {
			writeError(w, r, err)
			return
		}
		var req struct {
			Grant string `json:"grant"`
		}
		if err := decode(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		if s.opts.Config.Auth == "basic" || s.opts.Config.Auth == "none" {
			s.proxyPage(w, r, 401, "Open the Web UI from the board.")
			return
		}
		existingSession := singleProxyCookie(r, proxy.SessionCookie)
		dest, session, ok := s.proxyAuth.exchange(req.Grant, s.proxyOrigin(r), existingSession, time.Now())
		if !ok {
			s.proxyPage(w, r, 401, "This Web UI sign-in expired or has already been used. Open it again from the board.")
			return
		}
		if _, err := s.board.LiveProxyForward(r.Context(), board.System, dest.ForwardID); err != nil {
			s.proxyAuth.forgetForward(dest.ForwardID)
			if session != existingSession {
				s.proxyAuth.revokeSession(session)
			}
			s.proxyPage(w, r, 401, "This Web UI forward no longer exists.")
			return
		}
		if session != existingSession {
			s.setProxyCookie(w, r, proxy.SessionCookie, session)
		}
		s.setProxyCookie(w, r, proxy.ForwardCookie, dest.ForwardID)
		writeJSON(w, 200, proxyOpenResult{dest.URL})
	default:
		s.proxyPage(w, r, 404, "Unknown Web UI sign-in page.")
	}
}

// singleProxyCookie refuses ambiguous cookies instead of depending on their
// order (an application may otherwise shadow a root cookie at a deeper path).
func singleProxyCookie(r *http.Request, name string) string {
	value := ""
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			value = c.Value
			count++
		}
	}
	if count != 1 {
		return ""
	}
	return value
}

// authorizeProxy is shared by every selected-forward route, including future
// subdomain routing. The board bearer token never substitutes for a session.
func (s *Server) authorizeProxy(w http.ResponseWriter, r *http.Request) bool {
	switch s.opts.Config.Auth {
	case "none":
		return true
	case "basic":
		if s.basicAuthorized(r) {
			return true
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="omo", charset="UTF-8"`)
	default:
		if s.proxyAuth.authorized(singleProxyCookie(r, proxy.SessionCookie), time.Now()) {
			return true
		}
	}
	s.proxyPage(w, r, 401, "Sign in to the board and use Open to access this Web UI.")
	return false
}

// proxyPathID recognizes only a literal prefix and literal opaque id, matching
// the transport. Encoded separators cannot select a different route.
func proxyPathID(path string) string {
	rest, ok := strings.CutPrefix(path, "/proxy/")
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, "/")
	if len(id) != 32 {
		return ""
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return ""
		}
	}
	return id
}

func sameProxyOrigin(raw, origin string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.Host == "" {
		return false
	}
	want, err := url.Parse(origin)
	if err != nil {
		return false
	}
	port := func(v *url.URL) string {
		if p := v.Port(); p != "" {
			return p
		}
		if v.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return u.Scheme == want.Scheme && strings.EqualFold(u.Hostname(), want.Hostname()) && port(u) == port(want)
}

func (s *Server) boardWebSocket(r *http.Request) bool {
	// The mux's registered pattern identifies the two board-owned sockets;
	// /api as a whole must remain available to application WebSockets.
	_, pattern := s.mux.Handler(r)
	return pattern == "GET /api/agents/{id}/terminal" || pattern == "GET /api/terminals/{id}/socket"
}

// routeProxy runs after Host validation and before every board-only middleware.
// Only sign-in endpoints keep board Origin/JSON/CSP semantics; application
// Origin and CORS/WebSocket policy belongs to the selected upstream.
func (s *Server) routeProxy(w http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, proxyInternalPrefix) {
		s.serveProxyInternal(w, r)
		return true
	}
	if s.routeProxySubdomain(w, r) {
		return true
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && s.boardWebSocket(r) {
		return false
	}
	path := r.URL.EscapedPath()
	explicit := r.URL.Path == "/proxy" || strings.HasPrefix(r.URL.Path, "/proxy/")
	id := ""
	fallback := false
	ws := false
	if explicit {
		id = proxyPathID(path)
	} else {
		ref := r.Header.Get("Referer")
		if sameProxyOrigin(ref, s.proxyOrigin(r)) {
			u, _ := url.Parse(ref)
			id = proxyPathID(u.EscapedPath())
			fallback = id != "" && strings.HasPrefix(u.EscapedPath(), "/proxy/"+id+"/")
			if !fallback {
				id = ""
			}
		}
		if id == "" && ref == "" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			ws = true
			id = singleProxyCookie(r, proxy.ForwardCookie)
		}
	}
	if !explicit && !fallback && !ws {
		return false
	}
	if !s.authorizeProxy(w, r) {
		return true
	}
	f, err := s.board.LiveProxyForward(r.Context(), board.System, id)
	if err != nil {
		s.proxyAuth.forgetForward(id)
		s.proxyPage(w, r, 404, "This Web UI forward no longer exists.")
		return true
	}
	if fallback {
		location := f.BasePath + strings.TrimPrefix(path, "/")
		if r.URL.RawQuery != "" {
			location += "?" + r.URL.RawQuery
		}
		setSecurityHeaders(w)
		w.Header().Set("Location", location)
		w.WriteHeader(http.StatusTemporaryRedirect)
		return true
	}
	if explicit && path == strings.TrimSuffix(f.BasePath, "/") {
		location := f.BasePath
		if r.URL.RawQuery != "" {
			location += "?" + r.URL.RawQuery
		}
		setSecurityHeaders(w)
		w.Header().Set("Location", location)
		w.WriteHeader(307)
		return true
	}
	if ws {
		// The transport needs a path-mode URL. Preserve exact raw path and query.
		cloned := r.Clone(r.Context())
		u := *r.URL
		u.Path = strings.TrimSuffix(f.BasePath, "/") + r.URL.Path
		u.RawPath = strings.TrimSuffix(f.BasePath, "/") + path
		cloned.URL = &u
		r = cloned
	} else if r.Method == "GET" && (r.Header.Get("Sec-Fetch-Dest") == "document" || r.Header.Get("Sec-Fetch-Dest") == "") {
		s.setProxyCookie(w, r, proxy.ForwardCookie, f.ID)
	}
	s.serveProxyForward(w, r, f, proxy.PathMode)
	return true
}

func (s *Server) serveProxyForward(w http.ResponseWriter, r *http.Request, f api.ProxyForward, mode proxy.Mode) {
	tracked, done, ok := s.trackForwardRequest(w, r, f.ID, f.Plugin != "")
	if !ok {
		s.proxyPage(w, r, 404, "This Web UI forward no longer exists.")
		return
	}
	defer done()
	w = tracked
	origin, _ := url.Parse(s.proxyOrigin(r))
	// Same-origin app WebSockets need the upstream Host/Origin pair after Host
	// rewriting. Foreign origins remain untouched for the app to accept/refuse.
	if mode == proxy.SubdomainMode && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && r.Header.Get("Origin") != "" && s.sameProxyInternalOrigin(r) {
		r = r.Clone(r.Context())
		r.Header.Set("Origin", f.Scheme+"://"+net.JoinHostPort(f.Host, strconv.Itoa(f.Port)))
	}
	r = proxy.WithRequestContext(r, proxy.RequestContext{PublicScheme: origin.Scheme, PublicHost: origin.Host, BoardURL: s.proxyBoardURL(r), RemoveAuthorization: s.proxyAuthorizationIsOmo(r), OnError: func(error) { s.log.Printf("Web UI forward %s: upstream connection failed", f.ID) }})
	s.proxyTransport.Handler(f, mode).ServeHTTP(w, r)
}

func (s *Server) runProxySweep(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.proxyAuth.sweep(now)
			s.proxyAuth.mu.Lock()
			ids := make(map[string]bool)
			for _, grant := range s.proxyAuth.grants {
				ids[grant.destination.ForwardID] = true
			}
			s.proxyAuth.mu.Unlock()
			for id := range ids {
				if _, err := s.board.LiveProxyForward(ctx, board.System, id); err != nil {
					s.proxyAuth.forgetForward(id)
				}
			}
		}
	}
}
