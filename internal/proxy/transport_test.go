package proxy

import (
	"bytes"
	"compress/gzip"
	"context"

	"fmt"

	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func forwardFor(t *testing.T, address string) api.ProxyForward {
	t.Helper()
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return api.ProxyForward{ID: "abc", Host: u.Hostname(), Port: port, Scheme: u.Scheme, BasePath: "/proxy/abc/"}
}

func TestPaths(t *testing.T) {
	for _, tc := range []struct {
		name, path, want string
		mode             Mode
		strip            bool
	}{
		{"keep", "/proxy/abc/a%2fb//../c?x=%2f&x=1+2&bad=%zz", "/proxy/abc/a%2fb//../c?x=%2f&x=1+2&bad=%zz", PathMode, false},
		{"strip", "/proxy/abc/a%2fb//../c?x=%252f", "/a%2fb//../c?x=%252f", PathMode, true},
		{"encoded traversal", "/proxy/abc/%2e%2e/other%3fx?a=1", "/%2e%2e/other%3fx?a=1", PathMode, true},
		{"root", "/proxy/abc/?", "/?", PathMode, true},
		{"subdomain", "/a%2fb//../c?x=%2f", "/a%2fb//../c?x=%2f", SubdomainMode, true},
		{"subdomain keep", "/proxy/abc/a?x=2", "/proxy/abc/a?x=2", SubdomainMode, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.RequestURI) }))
			defer upstream.Close()
			tr := NewTransport()
			defer tr.CloseIdleConnections()
			f := forwardFor(t, upstream.URL)
			f.StripPrefix = tc.strip
			w := httptest.NewRecorder()
			tr.Handler(f, tc.mode).ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != 200 || w.Body.String() != tc.want {
				t.Fatalf("got %d %q, want %q", w.Code, w.Body.String(), tc.want)
			}
		})
	}
}

func TestRequestCredentialsAndForwarding(t *testing.T) {
	captured := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- r.Clone(context.Background())
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	for _, stripAuth := range []bool{false, true} {
		r := httptest.NewRequest("GET", "http://office.test/proxy/abc/a?app=%2f&__omo_proxy_grant=SECRET&b=1+2", nil)
		r.RemoteAddr = "192.0.2.8:1234"
		r.Header = http.Header{
			"Cookie":        {"app=first; omo_proxy_session=SECRET; omo_proxy_session =SECRET; omo_proxy_forward =SECRET; app=second", "omo_proxy_forward=SECRET; other=ok"},
			"Authorization": {"Bearer app-secret"}, "X-Omo-Session": {"SECRET"}, "X-Omo-Identity": {"SECRET"}, "Omo-Image-Name": {"SECRET"},
			"X-Omo-Proxy-Grant": {"SECRET"}, "X-Forwarded-For": {"attacker"}, "X-Forwarded-Host": {"attacker"}, "X-Forwarded-Proto": {"attacker"}, "X-Forwarded-Prefix": {"attacker"}, "Forwarded": {"for=attacker"},
			"X-Real-Ip": {"6.6.6.6"}, "True-Client-Ip": {"6.6.6.6"}, "Cf-Connecting-Ip": {"6.6.6.6"}, "X-Client-Ip": {"6.6.6.6"},
			"Sec-Websocket-Protocol": {"app-chat, omo-token.SECRET", "other"},
		}
		clientIPNames := []string{"X-Real-IP", "True-Client-IP", "CF-Connecting-IP", "CF-Connecting-IPv6", "X-Client-IP", "Client-IP", "X-Cluster-Client-IP", "Fastly-Client-IP", "Fly-Client-IP", "X-Envoy-External-Address"}
		for _, name := range clientIPNames {
			r.Header.Set(name, "6.6.6.6")
		}
		original := r.Header.Clone()
		r = WithRequestContext(r, RequestContext{PublicScheme: "https", PublicHost: "public.example:8443", BoardURL: "https://public.example:8443/", RemoveAuthorization: stripAuth})
		w := httptest.NewRecorder()
		tr.Handler(forwardFor(t, upstream.URL), PathMode).ServeHTTP(w, r)
		got := <-captured
		if strings.Contains(fmt.Sprint(got.Header), "SECRET") || strings.Contains(got.RequestURI, "SECRET") {
			t.Fatalf("credential leaked: %v %s", got.Header, got.RequestURI)
		}
		if got.Header.Get("Cookie") != "app=first; app=second" || len(got.Header.Values("Cookie")) != 2 || got.Header.Values("Cookie")[1] != "other=ok" {
			t.Fatalf("app cookies: %v", got.Header.Values("Cookie"))
		}
		wantAuth := "Bearer app-secret"
		if stripAuth {
			wantAuth = ""
		}
		if got.Header.Get("Authorization") != wantAuth {
			t.Fatalf("auth: %q", got.Header.Get("Authorization"))
		}
		for k, want := range map[string]string{"X-Forwarded-For": "192.0.2.8", "X-Forwarded-Host": "public.example:8443", "X-Forwarded-Proto": "https", "X-Forwarded-Prefix": "", "Sec-WebSocket-Protocol": "app-chat, other", "Forwarded": "", "X-Real-IP": "", "True-Client-IP": "", "CF-Connecting-IP": "", "X-Client-IP": ""} {
			if got.Header.Get(k) != want {
				t.Errorf("%s: %q want %q", k, got.Header.Get(k), want)
			}
		}
		for _, name := range clientIPNames {
			if got.Header.Get(name) != "" {
				t.Errorf("spoofed %s reached upstream", name)
			}
		}
		if got.RequestURI != "/proxy/abc/a?app=%2f&b=1+2" {
			t.Errorf("query: %s", got.RequestURI)
		}
		if fmt.Sprint(r.Header) != fmt.Sprint(original) {
			t.Fatal("mutated inbound headers")
		}
	}
}

func TestResponseRewrites(t *testing.T) {
	for _, mode := range []Mode{PathMode, SubdomainMode} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			var upstream *httptest.Server
			upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				locations := map[string]string{"absolute": upstream.URL + "/login?q=%2f#f", "protocol": "//" + r.Host + "/login", "root": "/login", "prefixed": "/proxy/abc/login", "relative": "../login?q=1", "external": "https://other.test/login", "boundary": "/proxy/abcd/login"}
				w.Header().Set("Location", locations[r.URL.Query().Get("location")])
				w.Header().Set("Cache-Control", "public, max-age=60")
				w.Header().Set("Content-Security-Policy", "default-src 'self'")
				for _, c := range []string{"app=a; Path=/; HttpOnly; SameSite=Lax", "app=b; Path=/proxy/abc/deep; Secure", "expired=; Path=/login; Domain=" + r.URL.Query().Get("domain") + "; Expires=Thu, 01 Jan 1970 00:00:00 GMT; Max-Age=0", "external=x; Domain=other.test; Path=/", "omo_proxy_session=overwrite; Path=/", "omo_proxy_forward =overwrite; Path=/"} {
					w.Header().Add("Set-Cookie", c)
				}
				w.WriteHeader(503)
				fmt.Fprint(w, "application failure")
			}))
			defer upstream.Close()
			tr := NewTransport()
			defer tr.CloseIdleConnections()
			f := forwardFor(t, upstream.URL)
			for key, location := range map[string]string{"absolute": "/proxy/abc/login?q=%2f#f", "protocol": "/proxy/abc/login", "root": "/proxy/abc/login", "prefixed": "/proxy/abc/login", "relative": "/proxy/abc/proxy/login?q=1", "external": "https://other.test/login", "boundary": "/proxy/abc/proxy/abcd/login"} {
				path := "/proxy/abc/start"
				if mode == SubdomainMode {
					path = "/start"
					switch key {
					case "relative":
						location = "../login?q=1"
					case "absolute":
						location = "https://abc.office.test/login?q=%2f#f"
					case "protocol":
						location = "https://abc.office.test/login"
					case "root":
						location = "/login"
					case "prefixed":
						location = "/proxy/abc/login"
					case "boundary":
						location = "/proxy/abcd/login"
					}
				}
				r := httptest.NewRequest("GET", path+"?location="+key+"&domain="+f.Host, nil)
				r = WithRequestContext(r, RequestContext{PublicHost: "abc.office.test", PublicScheme: "https"})
				w := httptest.NewRecorder()
				tr.Handler(f, mode).ServeHTTP(w, r)
				if w.Code != 503 || w.Body.String() != "application failure" || w.Header().Get("Location") != location {
					t.Errorf("%s: %d %q %q want %q", key, w.Code, w.Body.String(), w.Header().Get("Location"), location)
				}
				if w.Header().Get("Cache-Control") != "public, max-age=60" || w.Header().Get("Content-Security-Policy") != "default-src 'self'" {
					t.Fatal("changed upstream security/cache headers")
				}
				cookies := w.Result().Cookies()
				if len(cookies) != 4 {
					t.Fatalf("cookies: %v", w.Header().Values("Set-Cookie"))
				}
				wantRoot := "/proxy/abc/"
				wantLogin := "/proxy/abc/login"
				if mode == SubdomainMode {
					wantRoot = "/"
					wantLogin = "/login"
				}
				if cookies[0].Path != wantRoot || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[1].Path != "/proxy/abc/deep" || !cookies[1].Secure || cookies[2].Path != wantLogin || cookies[2].Domain != "" || cookies[2].MaxAge != -1 || cookies[2].Expires.IsZero() || cookies[3].Domain != "other.test" {
					t.Fatalf("cookies changed incorrectly: %v", w.Header().Values("Set-Cookie"))
				}
			}
		})
	}
}

func TestCredentialBearingRedirects(t *testing.T) {
	for _, route := range []struct {
		name  string
		mode  Mode
		strip bool
	}{
		{"path keep", PathMode, false},
		{"path strip", PathMode, true},
		{"subdomain keep", SubdomainMode, false},
		{"subdomain strip", SubdomainMode, true},
	} {
		t.Run(route.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, location, pathWant, subdomainWant string
			}{
				{"absolute", "http://user:pass@{target}/login?q=%2f&x=1+2#section%2Fone", "/proxy/abc/login?q=%2f&x=1+2#section%2Fone", "https://abc.office.test:8443/login?q=%2f&x=1+2#section%2Fone"},
				{"protocol relative", "//user:pass@{target}/login?q=%2f&x=1+2#section%2Fone", "/proxy/abc/login?q=%2f&x=1+2#section%2Fone", "https://abc.office.test:8443/login?q=%2f&x=1+2#section%2Fone"},
				{"username only", "http://user@{target}/login", "/proxy/abc/login", "https://abc.office.test:8443/login"},
				{"escaped credentials", "//us%40er:pa%3Ass%2Fword@{target}/a%2fb", "/proxy/abc/a%2fb", "https://abc.office.test:8443/a%2fb"},
				{"empty userinfo", "http://@{target}/login", "/proxy/abc/login", "https://abc.office.test:8443/login"},
				{"empty password", "//user:@{target}/login", "/proxy/abc/login", "https://abc.office.test:8443/login"},
				{"empty path", "http://user:pass@{target}", "/proxy/abc/", "https://abc.office.test:8443"},
				{"query without path", "//user:pass@{target}?q=%2f#part", "/proxy/abc/?q=%2f#part", "https://abc.office.test:8443?q=%2f#part"},
				{"dot segments", "http://user:pass@{target}/a/%2e%2e/login", "/proxy/abc/login", "https://abc.office.test:8443/a/%2e%2e/login"},
				{"already prefixed", "//user:pass@{target}/proxy/abc/login", "/proxy/abc/login", "https://abc.office.test:8443/proxy/abc/login"},
				{"query and fragment backslashes", `http://user:pass@{target}/login?q=keep\value#keep\fragment`, `/proxy/abc/login?q=keep\value#keep%5Cfragment`, `https://abc.office.test:8443/login?q=keep\value#keep%5Cfragment`},
				{"external absolute", "https://us%40er:pa%3Ass@other.test/login?q=%2f#part", "https://us%40er:pa%3Ass@other.test/login?q=%2f#part", "https://us%40er:pa%3Ass@other.test/login?q=%2f#part"},
				{"external protocol relative", "//user:pass@other.test/login?q=%2f#part", "//user:pass@other.test/login?q=%2f#part", "//user:pass@other.test/login?q=%2f#part"},
				{"non HTTP scheme", "ftp://user:pass@{target}/login", "ftp://user:pass@{target}/login", "ftp://user:pass@{target}/login"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Location", strings.ReplaceAll(tc.location, "{target}", r.Host))
						w.WriteHeader(http.StatusTemporaryRedirect)
						fmt.Fprint(w, "application redirect")
					}))
					defer upstream.Close()
					tr := NewTransport()
					defer tr.CloseIdleConnections()
					f := forwardFor(t, upstream.URL)
					f.StripPrefix = route.strip
					path, want := "/proxy/abc/start", tc.pathWant
					if route.mode == SubdomainMode {
						path, want = "/start", tc.subdomainWant
					}
					want = strings.ReplaceAll(want, "{target}", strings.TrimPrefix(upstream.URL, "http://"))
					r := WithRequestContext(httptest.NewRequest("GET", path, nil), RequestContext{
						PublicScheme: "https", PublicHost: "abc.office.test:8443",
						OnError: func(error) { t.Error("redirect unexpectedly reported as upstream failure") },
					})
					w := httptest.NewRecorder()
					tr.Handler(f, route.mode).ServeHTTP(w, r)
					if w.Code != http.StatusTemporaryRedirect || w.Body.String() != "application redirect" {
						t.Fatalf("upstream response changed: status=%d body=%q", w.Code, w.Body.String())
					}
					if got := w.Header().Get("Location"); got != want {
						t.Errorf("Location=%q want %q", got, want)
					}
				})
			}
		})
	}
}

func TestTLSAndUnavailable(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "secure") }))
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	f := forwardFor(t, upstream.URL)
	for _, verify := range []bool{false, true} {
		f.VerifyTLS = verify
		w := httptest.NewRecorder()
		r := WithRequestContext(httptest.NewRequest("GET", "/proxy/abc/", nil), RequestContext{BoardURL: "https://board.test/"})
		tr.Handler(f, PathMode).ServeHTTP(w, r)
		if !verify && (w.Code != 200 || w.Body.String() != "secure") {
			t.Fatalf("self-signed default: %d %s", w.Code, w.Body.String())
		}
		if verify && (w.Code != 502 || !strings.Contains(w.Body.String(), "https://board.test/") || !strings.Contains(w.Body.String(), f.Host)) {
			t.Fatalf("verified: %d %s", w.Code, w.Body.String())
		}
	}
	// A genuinely unreachable target produces the same clear page; application 503 was preserved above.
	upstream.Close()
	w := httptest.NewRecorder()
	tr.Handler(f, PathMode).ServeHTTP(w, httptest.NewRequest("GET", "/proxy/abc/", nil))
	if w.Code != 502 || !strings.Contains(w.Body.String(), "unreachable") {
		t.Fatalf("dead target: %d %s", w.Code, w.Body.String())
	}
}

func TestIPv6(t *testing.T) {
	l, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 unavailable: %v", err)
	}
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Host) }))
	upstream.Listener.Close()
	upstream.Listener = l
	upstream.Start()
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	w := httptest.NewRecorder()
	tr.Handler(forwardFor(t, upstream.URL), PathMode).ServeHTTP(w, httptest.NewRequest("GET", "/proxy/abc/", nil))
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "[::1]:") {
		t.Fatalf("IPv6: %d %q", w.Code, w.Body.String())
	}
}

func TestAcceptedOriginDefaults(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", upstream.URL+"/login")
		w.WriteHeader(302)
	}))
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	w := httptest.NewRecorder()
	tr.Handler(forwardFor(t, upstream.URL), SubdomainMode).ServeHTTP(w, httptest.NewRequest("GET", "https://abc.office.test/start", nil))
	if got := w.Header().Get("Location"); got != "https://abc.office.test/login" {
		t.Fatalf("accepted origin lost: %q", got)
	}
}

func TestPrefixSelectionRejectsEncodedAliases(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("wrong prefix reached upstream") }))
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	h := tr.Handler(forwardFor(t, upstream.URL), PathMode)
	for _, path := range []string{"/proxy/abcd/", "/proxy/%61bc/", "/proxy/abc%2fother/", "/other/"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
}

func TestRedirectAndCookieTables(t *testing.T) {
	target, _ := url.Parse("http://[::1]:80")
	for _, tc := range []struct{ in, want string }{
		{"http://[::1]/a%2fb?q=%2f#hash", "/proxy/abc/a%2fb?q=%2f#hash"},
		{"//[::1]/", "/proxy/abc/"},
		{"http://[::1]:8080/a", "http://[::1]:8080/a"},
		{"/proxy/abc?x=1", "/proxy/abc?x=1"},
		{"/proxy/%61bc/", "/proxy/abc/proxy/%61bc/"},
		{"?q=1", "?q=1"}, {"#section", "#section"}, {"//outside.test/a", "//outside.test/a"},
	} {
		if got := rewriteLocation(tc.in, target, "/proxy/abc", PathMode, RequestContext{}); got != tc.want {
			t.Errorf("Location %q: %q want %q", tc.in, got, tc.want)
		}
	}
	for _, tc := range []struct{ in, want string }{
		{"app=x; Path=/a; Priority=High; Partitioned", "app=x; Path=/proxy/abc/a; Priority=High; Partitioned"},
		{"app=x; Path=/proxy/abc/; Max-Age=0", "app=x; Path=/proxy/abc/; Max-Age=0"},
		{"app=x; Path=/proxy/abcd", "app=x; Path=/proxy/abc/proxy/abcd"},
		{"app=x; Domain=.DEV.LOCAL; Path=/", "app=x; Path=/proxy/abc/"},
		{"app=x; HttpOnly", "app=x; HttpOnly"},
		{"omo_proxy_forward=overwrite; Max-Age=0", ""},
	} {
		if got := rewriteCookie(tc.in, "app.dev.local", "/proxy/abc", PathMode); got != tc.want {
			t.Errorf("Cookie %q: %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestApplicationStatusesAndConnectionReuse(t *testing.T) {
	captured := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- r.RemoteAddr
		status, _ := strconv.Atoi(r.URL.Query().Get("status"))
		w.Header().Set("ETag", `"upstream"`)
		w.Header().Add("Vary", "Accept")
		w.Header().Add("Vary", "Accept-Language")
		w.Header().Set("Content-Encoding", "custom-app-encoding")
		w.Header().Set("Cache-Control", "private, max-age=120")
		w.WriteHeader(status)
		fmt.Fprint(w, "unchanged bytes")
	}))
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	f := forwardFor(t, upstream.URL)
	var remote string
	for _, status := range []int{201, 400, 401, 404, 500, 503} {
		w := httptest.NewRecorder()
		tr.Handler(f, PathMode).ServeHTTP(w, httptest.NewRequest("GET", "/proxy/abc/?status="+strconv.Itoa(status), nil))
		gotRemote := <-captured
		if remote != "" && remote != gotRemote {
			t.Fatalf("new handler failed to reuse connection: %s != %s", remote, gotRemote)
		}
		remote = gotRemote
		if w.Code != status || w.Body.String() != "unchanged bytes" || w.Header().Get("ETag") != `"upstream"` || len(w.Header().Values("Vary")) != 2 || w.Header().Get("Content-Encoding") != "custom-app-encoding" || w.Header().Get("Cache-Control") != "private, max-age=120" {
			t.Fatalf("application response changed: %d %v %s", w.Code, w.Header(), w.Body.String())
		}
	}
}

func TestAuthorizationHook(t *testing.T) {
	captured := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { captured <- r.Header.Get("Authorization") }))
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	for _, auth := range []string{"Bearer app-key", "Basic YXBwOnNlY3JldA=="} {
		for _, remove := range []bool{false, true} {
			r := httptest.NewRequest("GET", "/proxy/abc/", nil)
			r.Header.Set("Authorization", auth)
			r = WithRequestContext(r, RequestContext{RemoveAuthorization: remove})
			tr.Handler(forwardFor(t, upstream.URL), PathMode).ServeHTTP(httptest.NewRecorder(), r)
			want := auth
			if remove {
				want = ""
			}
			if got := <-captured; got != want {
				t.Fatalf("auth=%q remove=%t: %q", auth, remove, got)
			}
		}
	}
}

func TestCompressedResponseAndEscapedErrorPage(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	zw.Write([]byte("application body"))
	zw.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(compressed.Bytes())
	}))
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	w := httptest.NewRecorder()
	tr.Handler(forwardFor(t, upstream.URL), PathMode).ServeHTTP(w, httptest.NewRequest("GET", "/proxy/abc/", nil))
	if w.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(w.Body.Bytes(), compressed.Bytes()) {
		t.Fatal("upstream compressed bytes changed")
	}
	var page bytes.Buffer
	if err := unavailablePage.Execute(&page, struct{ Target, Board string }{`http://<script>bad()</script>:80`, `https://board.test/?x="&y=<unsafe>`}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page.String(), "<script>") || strings.Contains(page.String(), "<unsafe>") || !strings.Contains(page.String(), "&lt;script&gt;") || !strings.Contains(page.String(), "Back to the board") {
		t.Fatalf("unsafe error page: %s", page.String())
	}
}

func TestRedirectResolutionStaysInsideForward(t *testing.T) {
	for _, tc := range []struct {
		location, path, want string
		strip                bool
	}{
		{"../login", "/proxy/abc/start", "/proxy/abc/login", true},
		{"child?x=%2f#f", "/proxy/abc/start", "/proxy/abc/child?x=%2f#f", true},
		{"../login", "/proxy/abc/dir/start", "/proxy/abc/login", false},
		{"%2Flogin", "/proxy/abc/start", "/proxy/abc/%2Flogin", true},
		{"/../../login", "/proxy/abc/start", "/proxy/abc/login", true},
		{"/%2e%2e/%2E./login", "/proxy/abc/start", "/proxy/abc/login", true},
		{"/a/%2e%2e/login", "/proxy/abc/start", "/proxy/abc/login", false},
		{"/a/./b/../", "/proxy/abc/start", "/proxy/abc/a/", true},
		{"/a//b", "/proxy/abc/start", "/proxy/abc/a//b", true},
	} {
		t.Run(tc.location+fmt.Sprint(tc.strip), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", tc.location)
				w.WriteHeader(302)
			}))
			defer upstream.Close()
			tr := NewTransport()
			defer tr.CloseIdleConnections()
			f := forwardFor(t, upstream.URL)
			f.StripPrefix = tc.strip
			w := httptest.NewRecorder()
			tr.Handler(f, PathMode).ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if got := w.Header().Get("Location"); got != tc.want {
				t.Fatalf("Location=%q want %q", got, tc.want)
			}
		})
	}
}

func TestMixedDotRedirects(t *testing.T) {
	for _, route := range []struct {
		name  string
		mode  Mode
		strip bool
	}{
		{"path keep", PathMode, false},
		{"path strip", PathMode, true},
		{"subdomain keep", SubdomainMode, false},
		{"subdomain strip", SubdomainMode, true},
	} {
		t.Run(route.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, location, keepWant, stripWant, subdomainWant string
			}{
				{"root parent", "/a/%2e%2e/../login", "/proxy/abc/login", "/proxy/abc/login", "/a/%2e%2e/../login"},
				{"root current", "/a/%2E/../login", "/proxy/abc/login", "/proxy/abc/login", "/a/%2E/../login"},
				{"root mixed parent", "/a/.%2e/../login", "/proxy/abc/login", "/proxy/abc/login", "/a/.%2e/../login"},
				{"root reverse mixed parent", "/a/%2e./../login", "/proxy/abc/login", "/proxy/abc/login", "/a/%2e./../login"},
				{"root trailing parent", "/a/%2e%2e/..", "/proxy/abc/", "/proxy/abc/", "/a/%2e%2e/.."},
				{"relative parent", "a/%2e%2e/../login", "/proxy/abc/login", "/proxy/abc/login", "a/%2e%2e/../login"},
				{"relative escape base", "../%2e%2e/login", "/proxy/abc/proxy/login", "/proxy/abc/login", "../%2e%2e/login"},
				{"relative trailing parent", "a/%2e%2e/..", "/proxy/abc/", "/proxy/abc/", "a/%2e%2e/.."},
				{"root escapes", "/a%2fb/%2e%2e/../c%2Fd//?x=%2f&x=1+2#part%2f", "/proxy/abc/c%2Fd//?x=%2f&x=1+2#part%2f", "/proxy/abc/c%2Fd//?x=%2f&x=1+2#part%2f", "/a%2fb/%2e%2e/../c%2Fd//?x=%2f&x=1+2#part%2f"},
				{"relative escapes", "a%2fb/%2e%2e/../c%2Fd//?x=%2f#part%2F", "/proxy/abc/c%2Fd//?x=%2f#part%2F", "/proxy/abc/c%2Fd//?x=%2f#part%2F", "a%2fb/%2e%2e/../c%2Fd//?x=%2f#part%2F"},
				{"absolute with userinfo", "http://user:pass@{target}/a/%2e%2e/../login?x=%2f#part", "/proxy/abc/login?x=%2f#part", "/proxy/abc/login?x=%2f#part", "https://abc.office.test/a/%2e%2e/../login?x=%2f#part"},
				{"protocol relative with userinfo", "//user:pass@{target}/a/%2e%2e/../login?x=%2f#part", "/proxy/abc/login?x=%2f#part", "/proxy/abc/login?x=%2f#part", "https://abc.office.test/a/%2e%2e/../login?x=%2f#part"},
				{"external", "https://user:pass@other.test/a/%2e%2e/../login?x=%2f#part", "https://user:pass@other.test/a/%2e%2e/../login?x=%2f#part", "https://user:pass@other.test/a/%2e%2e/../login?x=%2f#part", "https://user:pass@other.test/a/%2e%2e/../login?x=%2f#part"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Location", strings.ReplaceAll(tc.location, "{target}", r.Host))
						w.WriteHeader(http.StatusFound)
					}))
					defer upstream.Close()
					tr := NewTransport()
					defer tr.CloseIdleConnections()
					f := forwardFor(t, upstream.URL)
					f.StripPrefix = route.strip
					path, want := "/proxy/abc/dir/start", tc.keepWant
					if route.strip {
						want = tc.stripWant
					}
					if route.mode == SubdomainMode {
						path, want = "/dir/start", tc.subdomainWant
					}
					r := WithRequestContext(httptest.NewRequest("GET", path, nil), RequestContext{PublicScheme: "https", PublicHost: "abc.office.test"})
					w := httptest.NewRecorder()
					tr.Handler(f, route.mode).ServeHTTP(w, r)
					if got := w.Header().Get("Location"); w.Code != http.StatusFound || got != want {
						t.Fatalf("status=%d Location=%q want %q", w.Code, got, want)
					}
				})
			}
		})
	}
}

func TestHostCookieRemainsUsable(t *testing.T) {
	cookie := "__Host-app=key; Secure; HttpOnly; Path=/"
	if got := rewriteCookie(cookie, "localhost", "/proxy/abc", PathMode); got != cookie {
		t.Fatalf("__Host- cookie must retain Path=/: %q", got)
	}
}

func TestForwardedPrefixOnlyWhenRemoved(t *testing.T) {
	captured := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { captured <- r.Header.Clone() }))
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	for _, mode := range []Mode{PathMode, SubdomainMode} {
		for _, strip := range []bool{false, true} {
			f := forwardFor(t, upstream.URL)
			f.StripPrefix = strip
			r := httptest.NewRequest("GET", "/proxy/abc/", nil)
			r.Header.Set("X-Forwarded-Prefix", "/attacker")
			tr.Handler(f, mode).ServeHTTP(httptest.NewRecorder(), r)
			want := ""
			if mode == PathMode && strip {
				want = "/proxy/abc"
			}
			if got := (<-captured).Get("X-Forwarded-Prefix"); got != want {
				t.Errorf("mode=%v strip=%v: %q want %q", mode, strip, got, want)
			}
		}
	}
}

func TestBrowserRedirectSyntax(t *testing.T) {
	for _, tc := range []struct{ location, want string }{
		{"/.\t./.\t./x", ""},
		{`/a/..\..\x`, "/proxy/abc/x"},
		{`/a/..\..\x?q=keep\value#keep\fragment`, `/proxy/abc/x?q=keep\value#keep%5Cfragment`},
	} {
		t.Run(tc.location, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", tc.location)
				w.WriteHeader(302)
			}))
			defer upstream.Close()
			tr := NewTransport()
			defer tr.CloseIdleConnections()
			w := httptest.NewRecorder()
			tr.Handler(forwardFor(t, upstream.URL), PathMode).ServeHTTP(w, httptest.NewRequest("GET", "/proxy/abc/start", nil))
			if got := w.Header().Get("Location"); got != tc.want {
				t.Fatalf("Location=%q want %q", got, tc.want)
			}
		})
	}
}

func TestUnavailableReportsTLSFailure(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unverified request reached upstream") }))
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	f := forwardFor(t, upstream.URL)
	f.VerifyTLS = true
	reported := make(chan error, 1)
	r := WithRequestContext(httptest.NewRequest("GET", "/proxy/abc/", nil), RequestContext{BoardURL: "https://board.test/", OnError: func(err error) { reported <- err }})
	w := httptest.NewRecorder()
	tr.Handler(f, PathMode).ServeHTTP(w, r)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "connect to or verify") || !strings.Contains(w.Body.String(), f.Host) {
		t.Fatalf("TLS failure page: %d %s", w.Code, w.Body.String())
	}
	select {
	case err := <-reported:
		if err == nil || !strings.Contains(err.Error(), "certificate") {
			t.Fatalf("lost TLS error: %v", err)
		}
	default:
		t.Fatal("connection failure was not reported")
	}
}

func TestGrantQuerySeparators(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"a=1;__omo_proxy_grant=SECRET&b=%2f;;x=1", "a=1&b=%2f;;x=1"},
		{"%5F_omo_proxy_grant=SECRET;a=1;b=2", "a=1;b=2"},
		{"a=1&__omo_proxy_grant=SECRET;b=2", "a=1&b=2"},
		{"__omo_proxy_grant=SECRET", ""},
		{"a=1;b=2&&c=%252f;d=+", "a=1;b=2&&c=%252f;d=+"},
	} {
		if got := withoutGrant(tc.raw); got != tc.want {
			t.Errorf("grant filter %q: %q want %q", tc.raw, got, tc.want)
		}
	}
}

func TestAuthoritylessHTTPRedirects(t *testing.T) {
	for _, location := range []string{"http:/x", "http:../../x", "http:x", "HTTP:/x", "https:/x", "hTtPs:../../x", "http:", "https:///x"} {
		for _, mode := range []Mode{PathMode, SubdomainMode} {
			t.Run(location+fmt.Sprint(mode), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Location", location); w.WriteHeader(302) }))
				defer upstream.Close()
				tr := NewTransport()
				defer tr.CloseIdleConnections()
				w := httptest.NewRecorder()
				tr.Handler(forwardFor(t, upstream.URL), mode).ServeHTTP(w, httptest.NewRequest("GET", "/proxy/abc/a/b", nil))
				want := location
				if mode == PathMode {
					want = ""
				}
				if got := w.Header().Get("Location"); w.Code != 302 || got != want {
					t.Fatalf("status=%d Location=%q want %q", w.Code, got, want)
				}
			})
		}
	}
}
