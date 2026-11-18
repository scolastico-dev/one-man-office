// Package proxy streams requests to registered web UI forwards. It does not
// authenticate requests or register routes: the browser router must do both
// before invoking a selected forward's handler.
package proxy

import (
	"context"
	"crypto/tls"
	"html/template"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Mode specifies whether the browser address carries a forward prefix.
type Mode int

const (
	PathMode Mode = iota
	SubdomainMode
)

// These names are reserved for omo's authenticated browser router. Apps may
// neither receive nor overwrite these cookies. Grants normally travel through
// the router's fragment bootstrap; the query name is reserved defensively too.
const (
	SessionCookie = "omo_proxy_session"
	ForwardCookie = "omo_proxy_forward"
	GrantQuery    = "__omo_proxy_grant"
)

// RequestContext contains values established by the authenticated router, never
// copied from client forwarding headers. PublicScheme/PublicHost override the
// accepted request origin (e.g. public_url behind a TLS terminator). BoardURL
// is the accepted board origin, needed for a subdomain's back link.
// RemoveAuthorization must be set only when the router positively identifies
// Authorization as omo's credential; application credentials otherwise survive.
type RequestContext struct {
	PublicScheme        string
	PublicHost          string
	BoardURL            string
	RemoveAuthorization bool
}
type requestContextKey struct{}

// WithRequestContext attaches the authenticated router's accepted origin and
// credential decision without modifying the inbound request.
func WithRequestContext(r *http.Request, values RequestContext) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestContextKey{}, values))
}

func requestValues(r *http.Request) RequestContext {
	v, _ := r.Context().Value(requestContextKey{}).(RequestContext)
	if v.PublicScheme == "" {
		v.PublicScheme = "http"
		if r.TLS != nil {
			v.PublicScheme = "https"
		}
	}
	if v.PublicHost == "" {
		v.PublicHost = r.Host
	}
	if v.BoardURL == "" {
		v.BoardURL = v.PublicScheme + "://" + v.PublicHost + "/"
	}
	return v
}

// Transport owns exactly two reusable pools (verified and self-signed TLS),
// independent of how many forwards are registered. Call CloseIdleConnections
// at server shutdown. Environment HTTP proxies are deliberately not used: a
// registered target is reached directly, including non-loopback local hosts.
type Transport struct{ verified, local *http.Transport }

func NewTransport() *Transport {
	verified := http.DefaultTransport.(*http.Transport).Clone()
	verified.Proxy = nil
	verified.DisableCompression = true // Preserve upstream Content-Encoding and bytes.
	local := verified.Clone()
	local.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Explicit per-forward default for trusted local apps.
	return &Transport{verified: verified, local: local}
}
func (t *Transport) CloseIdleConnections() {
	t.verified.CloseIdleConnections()
	t.local.CloseIdleConnections()
}

// Handler returns an http.Handler for one registry record, safe for concurrent
// use. Invoke it only after authentication and forward selection on every
// request. It leaves all response security/cache headers under upstream control.
// PathMode requires the literal /proxy/<id> prefix, preserving encoded suffixes;
// SubdomainMode accepts unprefixed paths regardless of StripPrefix.
func (t *Transport) Handler(f api.ProxyForward, mode Mode) http.Handler {
	prefix := "/proxy/" + f.ID
	target := &url.URL{Scheme: f.Scheme, Host: net.JoinHostPort(f.Host, strconv.Itoa(f.Port))}
	pool := t.local
	if f.VerifyTLS {
		pool = t.verified
	}
	p := &httputil.ReverseProxy{
		Transport:     pool,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			in, out := pr.In, pr.Out
			out.URL.Scheme = target.Scheme
			out.URL.Host = target.Host
			out.Host = target.Host
			// Rewrite normally cleans invalid queries; preserve the app's exact bytes.
			out.URL.RawQuery = withoutGrant(in.URL.RawQuery)
			if mode == PathMode && f.StripPrefix {
				raw := strings.TrimPrefix(in.URL.EscapedPath(), prefix)
				if raw == "" {
					raw = "/"
				}
				out.URL.Path, _ = url.PathUnescape(raw)
				out.URL.RawPath = raw
			}
			filterRequest(out)
			v := requestValues(in)
			pr.Out = WithRequestContext(out, v)
			if v.RemoveAuthorization {
				out.Header.Del("Authorization")
			}
			host, _, err := net.SplitHostPort(in.RemoteAddr)
			if err == nil {
				out.Header.Set("X-Forwarded-For", host)
			}
			out.Header.Set("X-Forwarded-Proto", v.PublicScheme)
			out.Header.Set("X-Forwarded-Host", v.PublicHost)
			if mode == PathMode {
				out.Header.Set("X-Forwarded-Prefix", prefix)
			}
		},
		ModifyResponse: func(res *http.Response) error {
			v := requestValues(res.Request)
			if locations := res.Header.Values("Location"); len(locations) > 0 {
				res.Header.Del("Location")
				for _, s := range locations {
					res.Header.Add("Location", rewriteLocation(s, res.Request.URL, prefix, mode, v))
				}
			}
			cookies := res.Header.Values("Set-Cookie")
			res.Header.Del("Set-Cookie")
			for _, s := range cookies {
				if s = rewriteCookie(s, f.Host, prefix, mode); s != "" {
					res.Header.Add("Set-Cookie", s)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_ = unavailablePage.Execute(w, struct{ Target, Board string }{target.String(), requestValues(r).BoardURL})
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode != PathMode && mode != SubdomainMode {
			http.NotFound(w, r)
			return
		}
		if mode == PathMode && !hasPrefix(r.URL.EscapedPath(), prefix) {
			http.NotFound(w, r)
			return
		}
		p.ServeHTTP(w, r)
	})
}

var unavailablePage = template.Must(template.New("unavailable").Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Web UI unreachable</title></head><body><h1>Web UI unreachable</h1><p>Nothing is listening at {{.Target}}, or the connection could not be established. The forward remains registered; try again when the app is running.</p><p><a href="{{.Board}}">Back to the board</a></p></body></html>`))

func hasPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
func reservedCookie(name string) bool {
	name = strings.TrimSpace(name)
	return name == SessionCookie || name == ForwardCookie
}

func withoutGrant(raw string) string {
	parts := strings.Split(raw, "&")
	keep := parts[:0]
	for _, p := range parts {
		key, _, _ := strings.Cut(p, "=")
		decoded, _ := url.QueryUnescape(key)
		if decoded != GrantQuery {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, "&")
}

func filterRequest(r *http.Request) {
	for k := range r.Header {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-omo-") || strings.HasPrefix(lower, "omo-") || strings.HasPrefix(lower, "x-forwarded-") || lower == "forwarded" {
			delete(r.Header, k)
		}
	}
	cookies := r.Header.Values("Cookie")
	r.Header.Del("Cookie")
	for _, line := range cookies {
		keep := []string{}
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			name, _, _ := strings.Cut(part, "=")
			if !reservedCookie(name) && part != "" {
				keep = append(keep, part)
			}
		}
		if len(keep) > 0 {
			r.Header.Add("Cookie", strings.Join(keep, "; "))
		}
	}
	protocols := []string{}
	for _, line := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(line, ",") {
			p = strings.TrimSpace(p)
			if p != "" && !strings.HasPrefix(p, "omo-token.") {
				protocols = append(protocols, p)
			}
		}
	}
	r.Header.Del("Sec-WebSocket-Protocol")
	if len(protocols) > 0 {
		r.Header.Set("Sec-WebSocket-Protocol", strings.Join(protocols, ", "))
	}
}

func rewriteLocation(s string, target *url.URL, prefix string, mode Mode, v RequestContext) string {
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.User != nil {
		return s
	}
	same := u.Host != "" && sameTarget(u, target)
	if u.Host != "" && !same {
		return s
	}
	if u.Scheme != "" && !same {
		return s
	}
	if !same && u.EscapedPath() == "" {
		return s // Query/fragment-only references already use the browser path.
	}
	if mode == SubdomainMode {
		if same {
			u.Scheme = v.PublicScheme
			u.Host = v.PublicHost
		}
		return u.String()
	}
	// Resolve path-relative references at the path the upstream actually saw.
	// Normalize browser dot segments before adding the routing prefix, so a
	// redirect cannot navigate out of this forward after browser normalization.
	u = target.ResolveReference(u)
	raw := redirectPath(u.EscapedPath())
	if raw == "" {
		raw = "/"
	}
	if !hasPrefix(raw, prefix) {
		raw = prefix + raw
	}
	u.Path, _ = url.PathUnescape(raw)
	u.RawPath = raw
	u.Scheme = ""
	u.Host = ""
	return u.String()
}

// redirectPath normalizes literal and percent-encoded dot segments as browsers
// do for HTTP URLs. Other escapes (including encoded slashes), duplicate slashes
// and trailing slashes retain their exact spelling. Request paths never use it.
func redirectPath(raw string) string {
	parts := strings.Split(raw, "/")
	kept := make([]string, 0, len(parts))
	for i, part := range parts {
		decoded, _ := url.PathUnescape(part)
		switch decoded {
		case ".", "..":
			if decoded == ".." && len(kept) > 1 {
				kept = kept[:len(kept)-1]
			}
			if i == len(parts)-1 {
				kept = append(kept, "")
			}
		default:
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "/")
}

func sameTarget(u, target *url.URL) bool {
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	port := u.Port()
	if port == "" {
		scheme := u.Scheme
		if scheme == "" {
			scheme = target.Scheme
		}
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	}
	return strings.EqualFold(u.Hostname(), target.Hostname()) && port == target.Port()
}

func rewriteCookie(s, host, prefix string, mode Mode) string {
	parts := strings.Split(s, ";")
	name, _, _ := strings.Cut(strings.TrimSpace(parts[0]), "=")
	if reservedCookie(name) {
		return ""
	}
	keep := parts[:1]
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(key, "Domain") {
			domain := strings.TrimPrefix(strings.ToLower(value), ".")
			h := strings.ToLower(host)
			if h == domain || strings.HasSuffix(h, "."+domain) {
				continue
			}
		}
		// __Host- cookies require Path=/ to be accepted by browsers. The apps
		// are trusted, so retain that shared root scope instead of breaking auth.
		hostCookie := strings.HasPrefix(strings.TrimSpace(name), "__Host-") && value == "/"
		if ok && strings.EqualFold(key, "Path") && mode == PathMode && !hostCookie && strings.HasPrefix(value, "/") && !hasPrefix(value, prefix) {
			part = " Path=" + prefix + value
		}
		keep = append(keep, part)
	}
	return strings.Join(keep, ";")
}
