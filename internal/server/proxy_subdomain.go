package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/proxy"
)

// proxyHost recognizes only the configured base and one exact composed label.
// Registry lookup is separate: an unknown well-formed id gets the missing page.
func (s *Server) proxyHost(host string) (id string, base bool) {
	c := s.opts.Config
	if c.ProxyBaseHost == "" {
		return "", false
	}
	return c.ProxyForwardID(host), strings.EqualFold(host, c.ProxyBaseHost)
}

func (s *Server) proxyHostPort(r *http.Request) bool {
	host, port, hasPort, ok := splitHostHeader(r.Host)
	if !ok {
		return false
	}
	id, base := s.proxyHost(host)
	if id == "" && !base {
		return false
	}
	defaultPort := "80"
	if r.TLS != nil {
		defaultPort = "443"
	}
	if s.proxyPublicHost(host, port, hasPort) {
		return true
	}
	if !hasPort {
		port = defaultPort
	}
	bound, _ := s.boundAddress(r)
	return port == bound
}

// proxyPublicHost applies public_url's external port and scheme only to the
// configured namespace. Direct listener traffic keeps its own transport scheme.
func (s *Server) proxyPublicHost(host, port string, hasPort bool) bool {
	id, base := s.proxyHost(host)
	if id == "" && !base {
		return false
	}
	p, ok := s.opts.Config.Public()
	if !ok {
		return false
	}
	if !hasPort {
		port = p.DefaultPort()
	}
	return port == p.EffectivePort()
}

func (s *Server) proxyCookieDomain(r *http.Request) string {
	host, _, _, ok := splitHostHeader(r.Host)
	if !ok || s.opts.Config.ProxyBaseHost == "" {
		return ""
	}
	base := strings.ToLower(s.opts.Config.ProxyBaseHost)
	host = strings.ToLower(host)
	if host == base || strings.HasSuffix(host, "."+base) {
		return base
	}
	return ""
}

func (s *Server) proxySubdomainOrigin(boardOrigin, id string) string {
	u, _ := url.Parse(boardOrigin)
	u.Host = strings.ToLower(s.opts.Config.ProxyLabelPrefix + id + s.opts.Config.ProxyLabelSuffix + "." + s.opts.Config.ProxyBaseHost)
	original, _ := url.Parse(boardOrigin)
	if original.Port() != "" {
		u.Host = net.JoinHostPort(u.Host, original.Port())
	}
	return canonicalProxyOrigin(u.String())
}

func (s *Server) routeProxySubdomain(w http.ResponseWriter, r *http.Request) bool {
	host, _, _, _ := splitHostHeader(r.Host)
	id, _ := s.proxyHost(host)
	if id == "" {
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
	if r.Method == "GET" && (r.Header.Get("Sec-Fetch-Dest") == "document" || r.Header.Get("Sec-Fetch-Dest") == "") {
		s.setProxyCookie(w, r, proxy.ForwardCookie, f.ID)
	}
	s.serveProxyForward(w, r, f, proxy.SubdomainMode)
	return true
}

// Bootstrap/exchange may run on a forward origin behind TLS termination. Keep
// this exception local to sign-in endpoints; board sameOrigin remains unchanged.
func (s *Server) sameProxyInternalOrigin(r *http.Request) bool {
	host, _, _, _ := splitHostHeader(r.Host)
	if id, base := s.proxyHost(host); id == "" && !base {
		return s.sameOrigin(r)
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if r.Header.Get("Origin") == "" {
		return true
	}
	u, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	return sameProxyOrigin(r.Header.Get("Origin"), s.proxyOrigin(r))
}
