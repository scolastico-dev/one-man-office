package server

import (
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func TestHostCheckAcceptsOmoLocalhost(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	auth := bearer(s)
	for _, host := range []string{"omo.localhost:" + b.port, "localhost:" + b.port, "127.0.0.1:" + b.port, "OMO.localhost:" + b.port} {
		auth["Host"] = host
		wantStatus(t, b.do("GET", "/api/server", nil, auth).StatusCode, http.StatusOK)
	}
	for _, host := range []string{"evil.com:" + b.port, "omo.localhost:1", "omo.localhost", "evil.localhost:" + b.port, "192.0.2.1:" + b.port} {
		auth["Host"] = host
		resp := b.do("GET", "/api/server", nil, auth)
		wantStatus(t, resp.StatusCode, http.StatusForbidden)
		if code := errorCode(t, resp.Body); code != api.CodeForbidden {
			t.Fatalf("host %s: code %q", host, code)
		}
	}
	// The page itself is protected by the Host check too.
	wantStatus(t, b.do("GET", "/", nil, map[string]string{"Host": "evil.com:" + b.port}).StatusCode, http.StatusForbidden)
}

func TestHostCheckWhenExposed(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) { c.Host = "0.0.0.0" })
	b := newBrowser(t, s)
	auth := bearer(s)
	for host, want := range map[string]int{
		"192.0.2.1:" + b.port:     http.StatusOK,
		"omo.localhost:" + b.port: http.StatusOK,
		"evil.com:" + b.port:      http.StatusForbidden,
	} {
		auth["Host"] = host
		wantStatus(t, b.do("GET", "/api/server", nil, auth).StatusCode, want)
	}
}

func TestTokenRequiredOnAPI(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	resp := b.do("GET", "/api/server", nil, nil)
	wantStatus(t, resp.StatusCode, http.StatusUnauthorized)
	if code := errorCode(t, resp.Body); code != api.CodeUnauthorized {
		t.Fatalf("code %q", code)
	}
	resp = b.do("GET", "/api/server", nil, map[string]string{"Authorization": "Bearer " + "0123"})
	wantStatus(t, resp.StatusCode, http.StatusUnauthorized)
	wantStatus(t, b.do("GET", "/", nil, nil).StatusCode, http.StatusOK)
	// Reserved and unknown API routes are protected as well.
	wantStatus(t, b.do("POST", "/api/ai/ready", nil, nil).StatusCode, http.StatusUnauthorized)
	wantStatus(t, b.do("GET", "/api/nothing", nil, nil).StatusCode, http.StatusUnauthorized)
	// The session header does not replace the token on TCP.
	wantStatus(t, b.do("GET", "/api/me", nil, map[string]string{SessionHeader: "x"}).StatusCode, http.StatusUnauthorized)
}

func TestBrowserCallerIsAlwaysUser(t *testing.T) {
	s := newTestServer(t)
	sess := liveSession(t, s, api.RoleCEO)
	hdr := bearer(s)
	hdr[SessionHeader] = sess.ID
	resp := newBrowser(t, s).do("GET", "/api/me", nil, hdr)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if me := decodeBody[api.Me](t, resp.Body); me.Actor.Role != api.RoleUser {
		t.Fatalf("browser caller %q", me.Actor.Role)
	}
}

func TestWebSocketProtocolToken(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	hdr := map[string]string{"Upgrade": "websocket", "Connection": "Upgrade", "Sec-WebSocket-Protocol": "omo-terminal, omo-token." + s.token}
	// Reserved route: authenticated, then 501.
	wantStatus(t, b.do("GET", "/api/terminals/1/socket", nil, hdr).StatusCode, http.StatusNotImplemented)
	hdr["Sec-WebSocket-Protocol"] = "omo-token.wrong"
	wantStatus(t, b.do("GET", "/api/terminals/1/socket", nil, hdr).StatusCode, http.StatusUnauthorized)
}

func TestBasicAuthMode(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) {
		c.Auth = "basic"
		c.BasicAuth = "me:secret"
	})
	if s.token != "" {
		t.Fatal("basic mode loaded the login token")
	}
	b := newBrowser(t, s)
	resp := b.do("GET", "/api/server", nil, nil)
	wantStatus(t, resp.StatusCode, http.StatusUnauthorized)
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("no WWW-Authenticate header")
	}
	if code := errorCode(t, resp.Body); code != api.CodeUnauthorized {
		t.Fatalf("code %q", code)
	}
	wantStatus(t, b.do("GET", "/", nil, nil).StatusCode, http.StatusUnauthorized)
	basic := func(cred string) map[string]string {
		return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(cred))}
	}
	wantStatus(t, b.do("GET", "/api/server", nil, basic("me:wrong")).StatusCode, http.StatusUnauthorized)
	wantStatus(t, b.do("GET", "/api/server", nil, basic("me:secret")).StatusCode, http.StatusOK)
	wantStatus(t, b.do("GET", "/", nil, basic("me:secret")).StatusCode, http.StatusOK)
}

func TestAuthNoneMode(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) { c.Auth = "none" })
	wantStatus(t, newBrowser(t, s).do("GET", "/api/server", nil, nil).StatusCode, http.StatusOK)
}

func TestCrossSiteOriginRejected(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	hdr := bearer(s)
	hdr["Origin"] = "http://evil.com"
	wantStatus(t, b.do("GET", "/api/server", nil, hdr).StatusCode, http.StatusForbidden)
	hdr["Origin"] = "http://omo.localhost:" + b.port
	wantStatus(t, b.do("GET", "/api/server", nil, hdr).StatusCode, http.StatusOK)
	delete(hdr, "Origin")
	hdr["Sec-Fetch-Site"] = "cross-site"
	wantStatus(t, b.do("GET", "/api/server", nil, hdr).StatusCode, http.StatusForbidden)

	open := newTestServer(t, func(c *serverconfig.Config) { c.OriginCheck = false })
	hdr = bearer(open)
	hdr["Origin"] = "http://evil.com"
	wantStatus(t, newBrowser(t, open).do("GET", "/api/server", nil, hdr).StatusCode, http.StatusOK)
}
