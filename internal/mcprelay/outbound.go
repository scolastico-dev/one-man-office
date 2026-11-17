package mcprelay

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/mcpauth"
)

// The rule for requests omo sends because an upstream told it to: the
// authorization server metadata, client registration, token, refresh and
// revocation endpoints a server names, and every redirect. Such a request
// connects only to a public address (checked after name resolution), to
// the host of the server's own address, which the user configured and may
// be local, or to the proxy of omo's environment. A redirect is followed
// only on the same host and never from https to http, so omo's credentials
// never travel to another host or in clear text.

// guardedTransport is the transport of those requests for a server at
// serverURL.
func guardedTransport(serverURL string) http.RoundTripper {
	allowed := map[string]bool{}
	if u, err := url.Parse(serverURL); err == nil && u.Hostname() != "" {
		allowed[strings.ToLower(u.Hostname())] = true
	}
	for _, scheme := range []string{"http", "https"} {
		req := &http.Request{URL: &url.URL{Scheme: scheme, Host: "upstream.invalid"}}
		if u, err := http.ProxyFromEnvironment(req); err == nil && u != nil {
			allowed[strings.ToLower(u.Hostname())] = true
		}
	}
	plain := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	public := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: mcpauth.PublicOnly}
	t := http.DefaultTransport.(*http.Transport).Clone()
	// Sign-ins are rare; no connection is kept for the next one.
	t.DisableKeepAlives = true
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err == nil && allowed[strings.ToLower(strings.Trim(host, "[]"))] {
			return plain.DialContext(ctx, network, addr)
		}
		return public.DialContext(ctx, network, addr)
	}
	return t
}

// errRedirect refuses a redirect to another host or from https to http.
var errRedirect = errors.New("it redirects to another host or from https to http, which omo does not follow")

// sameOriginRedirect is the CheckRedirect of omo's clients for upstreams:
// at most ten hops, each on the host of the first request and never from
// https to http.
func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("it redirects too often")
	}
	if !sameOrigin(via[0].URL, req.URL) {
		return errRedirect
	}
	return nil
}

// sameOrigin reports whether to may receive what was meant for from: the
// same host and port, or the same host upgraded from http to https.
func sameOrigin(from, to *url.URL) bool {
	if !strings.EqualFold(from.Hostname(), to.Hostname()) {
		return false
	}
	switch {
	case from.Scheme == to.Scheme:
		return portOf(from) == portOf(to)
	case from.Scheme == "http" && to.Scheme == "https":
		return true
	}
	return false
}

func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}
