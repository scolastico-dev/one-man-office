package server

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func withPublic(url string) func(*serverconfig.Config) {
	return func(c *serverconfig.Config) { c.PublicURL = url }
}

func TestPublicURLHostCheck(t *testing.T) {
	type tc struct {
		host string
		want bool
	}
	cases := map[string][]tc{
		"https://omo.example.com/": {
			{"omo.example.com", true}, {"OMO.Example.com", true}, {"omo.example.com:443", true},
			{"omo.example.com:8443", false}, {"omo.example.com:80", false}, {"omo.example.com:8090", false},
			{"evil.example.com", false}, {"omo.example.com.evil.com", false}, {"evil-omo.example.com", false},
			{"example.com", false}, {"sub.omo.example.com", false},
			// The built-in names keep their rule.
			{"omo.localhost:8090", true}, {"omo.localhost", false},
		},
		"http://omo.example.com:9000/": {
			{"omo.example.com:9000", true}, {"omo.example.com", false}, {"omo.example.com:9001", false}, {"omo.example.com:80", false},
		},
		"http://box.lan/": {
			{"box.lan", true}, {"box.lan:80", true}, {"box.lan:8090", false},
		},
		"http://[2001:db8::7]:9000/": {
			{"[2001:db8::7]:9000", true}, {"[2001:db8::7]", false}, {"[2001:db8::8]:9000", false},
		},
	}
	for public, list := range cases {
		s := newTestServer(t, withPublic(public))
		for _, c := range list {
			if got := s.allowedHost(hostRequest(c.host, 8090)); got != c.want {
				t.Errorf("public_url %s, Host %q: got %v, want %v", public, c.host, got, c.want)
			}
		}
	}
	plain := newTestServer(t)
	if plain.allowedHost(hostRequest("omo.example.com", 8090)) {
		t.Error("a host is accepted without public_url or allowed_hosts")
	}
}

func TestPublicURLOriginRules(t *testing.T) {
	type tc struct {
		host, origin string
		want         bool
	}
	https := newTestServer(t, withPublic("https://omo.example.com/"))
	http1 := newTestServer(t, withPublic("http://omo.example.com:9000/"))
	for _, c := range []struct {
		name string
		s    *Server
		tc
	}{
		{"https public, https origin", https, tc{"omo.example.com", "https://omo.example.com", true}},
		{"https public, http origin", https, tc{"omo.example.com", "http://omo.example.com", true}},
		{"https public, other origin", https, tc{"omo.example.com", "https://evil.example.com", false}},
		{"https public, other port origin", https, tc{"omo.example.com", "https://omo.example.com:8443", false}},
		{"https public, https origin on a built-in host", https, tc{"omo.localhost:8090", "https://omo.localhost:8090", false}},
		{"https public, https origin on another host", https, tc{"evil.example.com", "https://evil.example.com", false}},
		{"http public, http origin", http1, tc{"omo.example.com:9000", "http://omo.example.com:9000", true}},
		{"http public, https origin is not enabled", http1, tc{"omo.example.com:9000", "https://omo.example.com:9000", false}},
		{"http public, other origin", http1, tc{"omo.example.com:9000", "http://evil.example.com:9000", false}},
	} {
		r := hostRequest(c.host, 8090)
		r.Header.Set("Origin", c.origin)
		if got := c.s.sameOrigin(r); got != c.want {
			t.Errorf("%s: Host %q Origin %q: got %v, want %v", c.name, c.host, c.origin, got, c.want)
		}
	}
}

func TestPublicURLEndToEndAndServerInfo(t *testing.T) {
	s := newTestServer(t, withPublic("https://omo.example.com/"))
	s.opts.Version = "0.11.0-476-g8a63736"
	b := newBrowser(t, s)
	auth := bearer(s)
	auth["Host"] = "omo.example.com"
	auth["Origin"] = "https://omo.example.com"
	resp := b.do("GET", "/api/server", nil, auth)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	info := decodeBody[api.ServerInfo](t, resp.Body)
	if info.Address != "https://omo.example.com/" || info.Version != "0.11.0-476-g8a63736" ||
		info.VersionURL != "https://github.com/scolastico-dev/one-man-office/commit/8a63736" || info.VersionCommit != "8a63736" {
		t.Fatalf("%+v", info)
	}
	auth["Host"] = "evil.example.com"
	auth["Origin"] = "https://evil.example.com"
	wantStatus(t, b.do("GET", "/api/server", nil, auth).StatusCode, http.StatusForbidden)

	// Links to the board follow the advertised address.
	p := plugins.TicketPayload(api.TicketSummary{Number: 7}, s.Address())
	if p["url"] != "https://omo.example.com/7" {
		t.Fatalf("ticket url %v", p["url"])
	}
}

func TestServerInfoVersionLinkForEveryForm(t *testing.T) {
	for version, want := range map[string][2]string{
		"0.12.0":              {"https://github.com/scolastico-dev/one-man-office/releases/tag/0.12.0", ""},
		"v0.12.0":             {"https://github.com/scolastico-dev/one-man-office/releases/tag/v0.12.0", ""},
		"0.11.0-476-g8a63736": {"https://github.com/scolastico-dev/one-man-office/commit/8a63736", "8a63736"},
		"dev":                 {"", ""},
		"0.12.0-dirty":        {"", ""},
	} {
		s := newTestServer(t)
		s.opts.Version = version
		b := newBrowser(t, s)
		info := decodeBody[api.ServerInfo](t, b.do("GET", "/api/server", nil, bearer(s)).Body)
		if info.VersionURL != want[0] || info.VersionCommit != want[1] {
			t.Errorf("%s: %+v", version, info)
		}
	}
}

func runCapture(t *testing.T, mutate ...func(*serverconfig.Config)) (out, ready string) {
	t.Helper()
	layout := testHome(t)
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := Run(ctx, Options{Layout: layout, Config: testConfig(mutate...), Version: "test"}, &buf, func(u string) error { ready = u; cancel(); return nil })
	if err != nil {
		t.Fatal(err)
	}
	return buf.String(), ready
}

func TestRunAdvertisesPublicURL(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	out, ready := runCapture(t, withPublic("https://omo.example.com/"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[0], "omo: https://omo.example.com/#") || len(lines[0]) != len("omo: https://omo.example.com/#")+64 {
		t.Fatalf("first line %q", lines[0])
	}
	if ready != strings.TrimPrefix(lines[0], "omo: ") {
		t.Errorf("ready %q", ready)
	}
	if !strings.HasPrefix(lines[1], "also: http://omo.localhost:") || !strings.HasPrefix(lines[2], "also: http://127.0.0.1:") {
		t.Errorf("listening addresses missing:\n%s", out)
	}
	if !strings.HasPrefix(lines[1], "also: http://omo.localhost:") || strings.Count(out, "omo.example.com") != 1 {
		t.Errorf("output:\n%s", out)
	}

	// Without the option nothing changes.
	out, _ = runCapture(t)
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[0], "omo: http://omo.localhost:") || !strings.HasPrefix(lines[1], "also: http://127.0.0.1:") {
		t.Errorf("default output:\n%s", out)
	}

	// No token, no fragment.
	out, _ = runCapture(t, withPublic("http://box.lan:9000"), func(c *serverconfig.Config) { c.Auth = "none" })
	if !strings.HasPrefix(out, "omo: http://box.lan:9000/\n") {
		t.Errorf("auth none output:\n%s", out)
	}
}

func TestRunRejectsInvalidPublicURL(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	err := Run(context.Background(), Options{Layout: testHome(t), Config: testConfig(withPublic("https://x.example/omo")), Version: "test"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "public_url") {
		t.Fatalf("got %v", err)
	}
}

func TestHostNameHintSkipsPublicHost(t *testing.T) {
	exposed := &net.TCPAddr{IP: net.IPv4zero, Port: 8090}
	var b strings.Builder
	printHostNameHint(&b, serverconfig.Config{PublicURL: "https://My-Laptop/"}, exposed, "my-laptop")
	if b.String() != "" {
		t.Errorf("hint printed for the advertised host: %q", b.String())
	}
}

func TestServerInfoListenAndConfigured(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) { c.Port = 8123 })
	b := newBrowser(t, s)
	info := decodeBody[api.ServerInfo](t, b.do("GET", "/api/server", nil, bearer(s)).Body)
	if info.Listen != "127.0.0.1:8123" || info.AddressConfigured {
		t.Fatalf("%+v", info)
	}
	s = newTestServer(t, withPublic("https://omo.example.com"))
	b = newBrowser(t, s)
	auth := bearer(s)
	auth["Host"] = "omo.example.com"
	info = decodeBody[api.ServerInfo](t, b.do("GET", "/api/server", nil, auth).Body)
	if !info.AddressConfigured || info.Address != "https://omo.example.com/" {
		t.Fatalf("%+v", info)
	}
}
