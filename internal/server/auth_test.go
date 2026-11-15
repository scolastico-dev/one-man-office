package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
	// Authenticated, then an unknown terminal.
	wantStatus(t, b.do("GET", "/api/terminals/1/socket", nil, hdr).StatusCode, http.StatusNotFound)
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

func TestForeignOriginRejectedOnWrites(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/projects/rescan", nil},
		{"PUT", "/api/server/paused", map[string]bool{"paused": true}},
	} {
		hdr := bearer(s)
		hdr["Origin"] = "http://evil.com"
		resp := b.do(c.method, c.path, c.body, hdr)
		wantStatus(t, resp.StatusCode, http.StatusForbidden)
		if code := errorCode(t, resp.Body); code != api.CodeForbidden {
			t.Fatalf("%s %s: code %q", c.method, c.path, code)
		}
	}
	if paused, _ := s.board.Paused(context.Background()); paused {
		t.Fatal("a foreign-origin PUT paused the server")
	}
	// The same requests pass with the server's own origin.
	hdr := bearer(s)
	hdr["Origin"] = "http://omo.localhost:" + b.port
	wantStatus(t, b.do("PUT", "/api/server/paused", map[string]bool{"paused": true}, hdr).StatusCode, http.StatusOK)
	wantStatus(t, b.do("POST", "/api/projects/rescan", map[string]any{}, hdr).StatusCode, http.StatusOK)
}

// The server has no websocket handler yet; the upgrade request goes through
// the Origin check in front of the terminal socket route.
func TestForeignOriginRejectedOnWebSocketUpgrade(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	hdr := map[string]string{
		"Upgrade":                "websocket",
		"Connection":             "Upgrade",
		"Sec-WebSocket-Protocol": "omo-terminal, omo-token." + s.token,
		"Origin":                 "http://evil.com",
	}
	resp := b.do("GET", "/api/terminals/1/socket", nil, hdr)
	wantStatus(t, resp.StatusCode, http.StatusForbidden)
	if code := errorCode(t, resp.Body); code != api.CodeForbidden {
		t.Fatalf("code %q", code)
	}
	hdr["Origin"] = "http://omo.localhost:" + b.port
	wantStatus(t, b.do("GET", "/api/terminals/1/socket", nil, hdr).StatusCode, http.StatusNotFound)
}

func TestHostCheckWhenExposedAcceptsLoopbackNames(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) { c.Host = "0.0.0.0" })
	b := newBrowser(t, s)
	auth := bearer(s)
	for _, host := range []string{"localhost", "omo.localhost", "127.0.0.1", "[::1]"} {
		auth["Host"] = host + ":" + b.port
		wantStatus(t, b.do("GET", "/api/server", nil, auth).StatusCode, http.StatusOK)
	}
}

// A Host header without a port never matches: the port of the connection
// is part of the check, so the request is refused with 403 in loopback and
// exposed mode alike.
func TestHostWithoutPortRejected(t *testing.T) {
	for _, mutate := range []func(*serverconfig.Config){
		func(*serverconfig.Config) {},
		func(c *serverconfig.Config) { c.Host = "0.0.0.0" },
	} {
		s := newTestServer(t, mutate)
		b := newBrowser(t, s)
		auth := bearer(s)
		for _, host := range []string{"localhost", "omo.localhost", "127.0.0.1", "[::1]", "192.0.2.1"} {
			auth["Host"] = host
			resp := b.do("GET", "/api/server", nil, auth)
			wantStatus(t, resp.StatusCode, http.StatusForbidden)
			if code := errorCode(t, resp.Body); code != api.CodeForbidden {
				t.Fatalf("host %q: code %q", host, code)
			}
		}
	}
}

// hostRequest builds a request that arrived on local port localPort.
func hostRequest(host string, localPort int) *http.Request {
	r := httptest.NewRequest("GET", "/api/server", nil)
	r.Host = host
	ctx := context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: localPort})
	return r.WithContext(ctx)
}

func TestAllowedHostRules(t *testing.T) {
	type tc struct {
		host string
		port int // local port of the connection
		want bool
	}
	run := func(t *testing.T, s *Server, cases []tc) {
		t.Helper()
		for _, c := range cases {
			if got := s.allowedHost(hostRequest(c.host, c.port)); got != c.want {
				t.Errorf("Host %q on port %d: got %v, want %v", c.host, c.port, got, c.want)
			}
		}
	}
	loopback := newTestServer(t)
	t.Run("loopback", func(t *testing.T) {
		run(t, loopback, []tc{
			{"omo.localhost:8090", 8090, true},
			{"OMO.Localhost:8090", 8090, true},
			{"localhost:8090", 8090, true},
			{"127.0.0.1:8090", 8090, true},
			{"[::1]:8090", 8090, true},
			{"omo.localhost", 80, true},
			{"localhost", 80, true},
			{"127.0.0.1", 80, true},
			{"[::1]", 80, true},
			{"omo.localhost:80", 80, true},
			{"omo.localhost", 8090, false},
			{"localhost", 8090, false},
			{"omo.localhost:8091", 8090, false},
			{"omo.localhost:80", 8090, false},
			{"omo.localhost:", 80, false},
			{"omo.localhost:http", 80, false},
			{"", 80, false},
			{"evil.com:8090", 8090, false},
			{"evil.com", 80, false},
			{"evil.localhost:8090", 8090, false},
			{"evil-omo.localhost:8090", 8090, false},
			{"omo.localhost.evil.com:8090", 8090, false},
			{"192.0.2.1:8090", 8090, false},
			{"192.0.2.1", 80, false},
			{"[2001:db8::1]:8090", 8090, false},
			{"my-laptop:8090", 8090, false},
			{"my-laptop", 80, false},
		})
	})
	exposed := newTestServer(t, func(c *serverconfig.Config) { c.Host = "0.0.0.0" })
	t.Run("exposed", func(t *testing.T) {
		run(t, exposed, []tc{
			{"192.0.2.1:8090", 8090, true},
			{"192.0.2.1", 80, true},
			{"[2001:db8::1]:8090", 8090, true},
			{"my-laptop:8090", 8090, false},
			{"MY-Laptop:8090", 8090, false},
			{"my-laptop.local:8090", 8090, false},
			{"box:8090", 8090, false},
			{"box.local:8090", 8090, false},
			{"my-laptop", 80, false},
			{"omo.localhost:8090", 8090, true},
			{"my-laptop", 8090, false},
			{"my-laptop:8091", 8090, false},
			{"192.0.2.1", 8090, false},
			{"192.0.2.1:8091", 8090, false},
			{"my-laptop.evil.com:8090", 8090, false},
			{"evil-my-laptop:8090", 8090, false},
			{"my-laptop.local.evil.com:8090", 8090, false},
			{"evil.com:8090", 8090, false},
			{"evil.com", 80, false},
			{"other:8090", 8090, false},
		})
	})
	exposedListed := newTestServer(t, func(c *serverconfig.Config) {
		c.Host = "0.0.0.0"
		c.AllowedHosts = []string{"my-laptop", "my-laptop.local", "box"}
	})
	t.Run("exposed with machine names listed", func(t *testing.T) {
		run(t, exposedListed, []tc{
			{"my-laptop:8090", 8090, true},
			{"MY-Laptop:8090", 8090, true},
			{"my-laptop.local:8090", 8090, true},
			{"box:8090", 8090, true},
			{"my-laptop", 80, true},
			{"box.local:8090", 8090, false},
		})
	})
	listed := newTestServer(t, func(c *serverconfig.Config) {
		c.AllowedHosts = []string{"omo.example", "Port.example:8443", "10.1.2.3", "[2001:db8::2]:9000"}
	})
	t.Run("allowed_hosts", func(t *testing.T) {
		run(t, listed, []tc{
			// A name without a port matches with any port or none.
			{"omo.example", 8090, true},
			{"omo.example:443", 8090, true},
			{"omo.example:1", 8090, true},
			{"OMO.example", 80, true},
			// A name with a port matches that port only.
			{"port.example:8443", 8090, true},
			{"PORT.example:8443", 80, true},
			{"port.example", 80, false},
			{"port.example", 8090, false},
			{"port.example:8444", 8090, false},
			{"port.example:443", 8090, false},
			{"10.1.2.3:5", 8090, true},
			{"[2001:db8::2]:9000", 8090, true},
			{"[2001:db8::2]:9001", 8090, false},
			{"[2001:db8::2]", 80, false},
			// Names that merely end with, start with or contain an entry.
			{"evil-omo.example", 8090, false},
			{"omo.example.evil.com", 8090, false},
			{"x.omo.example", 8090, false},
			{"omo.example.", 8090, false},
			{"evil.com", 8090, false},
			// An entry does not enable the machine name or foreign IPs.
			{"192.0.2.1:8090", 8090, false},
		})
	})
}

func TestOriginRules(t *testing.T) {
	type tc struct {
		host, origin string
		tls          bool
		want         bool
	}
	s := newTestServer(t, func(c *serverconfig.Config) {
		c.AllowedHosts = []string{"omo.example", "port.example:8443"}
	})
	for _, c := range []tc{
		{"omo.localhost:8090", "http://omo.localhost:8090", false, true},
		{"omo.localhost:8090", "HTTP://OMO.localhost:8090", false, true},
		{"omo.localhost", "http://omo.localhost", false, true},
		{"omo.localhost", "http://omo.localhost:80", false, true},
		{"omo.localhost:80", "http://omo.localhost", false, true},
		{"omo.localhost:8090", "", false, true},
		{"omo.localhost:8090", "http://omo.localhost", false, false},
		{"omo.localhost:8090", "http://omo.localhost:8091", false, false},
		{"omo.localhost:8090", "http://evil.com", false, false},
		{"omo.localhost:8090", "http://evil.com:8090", false, false},
		{"omo.localhost:8090", "null", false, false},
		{"omo.localhost:8090", "http://omo.localhost:8090/", false, false},
		{"omo.localhost:8090", "http://omo.localhost:8090/x", false, false},
		{"omo.localhost:8090", "http://user@omo.localhost:8090", false, false},
		{"omo.localhost:8090", "ftp://omo.localhost:8090", false, false},
		// https only for a TLS connection or an allowed_hosts entry.
		{"omo.localhost:8090", "https://omo.localhost:8090", false, false},
		{"omo.localhost:8090", "https://omo.localhost:8090", true, true},
		{"omo.localhost:8090", "http://omo.localhost:8090", true, false},
		{"localhost", "https://localhost", false, false},
		{"omo.example", "https://omo.example", false, true},
		{"omo.example", "http://omo.example", false, true},
		{"omo.example:8443", "https://omo.example:8443", false, true},
		{"omo.example", "https://omo.example:444", false, false},
		{"omo.example", "https://evil.com", false, false},
		{"omo.example", "https://evil-omo.example", false, false},
		{"port.example:8443", "https://port.example:8443", false, true},
		{"port.example:8443", "https://port.example", false, false},
		{"port.example:8443", "http://port.example:8443", false, true},
	} {
		r := hostRequest(c.host, 8090)
		if c.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := s.sameOrigin(r); got != c.want {
			t.Errorf("Host %q Origin %q tls=%v: got %v, want %v", c.host, c.origin, c.tls, got, c.want)
		}
	}
}

func TestHostCheckWithAllowedHostsEndToEnd(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) { c.AllowedHosts = []string{"omo.example"} })
	b := newBrowser(t, s)
	auth := bearer(s)
	auth["Host"] = "omo.example"
	auth["Origin"] = "https://omo.example"
	wantStatus(t, b.do("GET", "/api/server", nil, auth).StatusCode, http.StatusOK)
	auth["Host"] = "omo.example:" + b.port
	auth["Origin"] = "http://omo.example:" + b.port
	wantStatus(t, b.do("GET", "/api/server", nil, auth).StatusCode, http.StatusOK)
	auth["Host"] = "evil.example"
	auth["Origin"] = "https://evil.example"
	wantStatus(t, b.do("GET", "/api/server", nil, auth).StatusCode, http.StatusForbidden)
}

func TestHostNameHint(t *testing.T) {
	exposed := &net.TCPAddr{IP: net.IPv4zero, Port: 8090}
	loopback := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8090}
	hint := func(c serverconfig.Config, addr *net.TCPAddr, name string) string {
		var b strings.Builder
		printHostNameHint(&b, c, addr, name)
		return b.String()
	}
	if got, want := hint(serverconfig.Config{}, exposed, "My-Laptop.example.org"),
		"To open omo as http://My-Laptop.example.org:8090, run: omo config set allowed_hosts My-Laptop.example.org\n"; got != want {
		t.Errorf("exposed: got %q, want %q", got, want)
	}
	if got := hint(serverconfig.Config{AllowedHosts: []string{"a.example", "b.example:8443"}}, exposed, "box"); !strings.Contains(got, "allowed_hosts a.example,b.example:8443,box\n") {
		t.Errorf("existing entries not kept: %q", got)
	}
	if got := hint(serverconfig.Config{}, loopback, "box"); got != "" {
		t.Errorf("loopback printed %q", got)
	}
	if got := hint(serverconfig.Config{}, exposed, ""); got != "" {
		t.Errorf("empty name printed %q", got)
	}
	for _, listed := range []string{"box", "BOX", "box:8090"} {
		if got := hint(serverconfig.Config{AllowedHosts: []string{listed}}, exposed, "box"); got != "" {
			t.Errorf("allowed via %q still printed %q", listed, got)
		}
	}
	if got := hint(serverconfig.Config{AllowedHosts: []string{"box:9999"}}, exposed, "box"); got == "" {
		t.Error("entry for another port should not cover the name")
	}
}

func TestRunPrintsHostNameHintWhenExposed(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	old := hostName
	hostName = func() (string, error) { return "my-laptop", nil }
	t.Cleanup(func() { hostName = old })
	run := func(host string) string {
		layout := testHome(t)
		c := testConfig(func(c *serverconfig.Config) { c.Host = host; c.Port = 0 })
		var out bytes.Buffer
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := Run(ctx, Options{Layout: layout, Config: c, Version: "test"}, &out, func(string) error { cancel(); return nil })
		if err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := run("0.0.0.0"); !strings.Contains(got, "omo config set allowed_hosts my-laptop\n") {
		t.Errorf("exposed output lacks the hint:\n%s", got)
	} else {
		t.Log(got)
	}
	if got := run("127.0.0.1"); strings.Contains(got, "allowed_hosts") {
		t.Errorf("loopback output has the hint:\n%s", got)
	}
}
