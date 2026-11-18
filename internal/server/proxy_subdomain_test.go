package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func subdomainConfig(c *serverconfig.Config) {
	c.Port = 8090
	c.ProxyBaseHost = "apps.localhost"
	c.ProxyLabelPrefix = "dev-"
	c.ProxyLabelSuffix = "-user"
}

func subdomainRequest(t *testing.T, s *Server, method, raw, body string, h http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, raw, strings.NewReader(body))
	req.TLS = nil // TLS terminates outside omo in these tests.
	req.Header = h.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	rec := httptest.NewRecorder()
	s.BrowserHandler().ServeHTTP(rec, req)
	return rec
}

func TestProxySubdomainHosts(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	for _, public := range []string{"", "https://board.example", "https://board.example:8443"} {
		name := "listener"
		if public != "" {
			name = "public-" + strings.ReplaceAll(public, ":", "-")
		}
		t.Run(name, func(t *testing.T) {
			s := newTestServer(t, subdomainConfig, func(c *serverconfig.Config) { c.PublicURL = public })
			port := ":8090"
			if public != "" {
				port = ""
			}
			if strings.Contains(public, "8443") {
				port = ":8443"
			}
			for _, tc := range []struct {
				host string
				want bool
			}{
				{"dev-" + id + "-user.apps.localhost" + port, true},
				{"DEV-" + strings.ToUpper(id) + "-USER.APPS.LOCALHOST" + port, true},
				{"apps.localhost" + port, true}, {"omo.localhost:8090", true},
				{"dev-" + id + "-user.apps.localhost:9999", false},
				{id + ".apps.localhost" + port, false}, {"dev-" + id + "-other.apps.localhost" + port, false},
				{"dev-" + id + "-user.extra.apps.localhost" + port, false},
				{"dev-" + strings.Repeat("g", 32) + "-user.apps.localhost" + port, false},
				{"dev-" + id + "-user.apps.localhost.evil" + port, false},
				{"dev-" + id + "-user.apps.localhost." + port, false},
			} {
				req := httptest.NewRequest("GET", "http://"+tc.host+"/", nil)
				if got := s.allowedHost(req); got != tc.want {
					t.Errorf("%s: %v want %v", tc.host, got, tc.want)
				}
			}
			// Allowing proxy Hosts must not permit them as board Origins.
			req := httptest.NewRequest("POST", "http://omo.localhost:8090/api/tickets", nil)
			req.Header.Set("Origin", "http://dev-"+id+"-user.apps.localhost:8090")
			if s.sameOrigin(req) {
				t.Fatal("proxy origin accepted on board")
			}
		})
	}
	s := newTestServer(t)
	if s.allowedHost(httptest.NewRequest("GET", "http://"+id+".apps.localhost:8090/", nil)) {
		t.Fatal("disabled mode accepted host")
	}
}

func TestProxySubdomainOpenAndSharedCookies(t *testing.T) {
	for _, base := range []string{"apps.localhost", "apps.example"} {
		for _, public := range []string{"", "https://board.example:8443"} {
			t.Run(base+public, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Path", r.URL.RequestURI())
					w.Header().Set("X-Cookies", r.Header.Get("Cookie"))
					w.Header().Set("X-Auth", r.Header.Get("Authorization"))
					w.Header().Set("X-Forwarded", r.Header.Get("X-Forwarded-Proto")+"://"+r.Header.Get("X-Forwarded-Host"))
					w.Write([]byte("app"))
				}))
				defer upstream.Close()
				s := newTestServer(t, subdomainConfig, func(c *serverconfig.Config) { c.ProxyBaseHost = base; c.PublicURL = public })
				f := proxyForward(t, s, upstream.URL, false)
				f2 := proxyForward(t, s, upstream.URL, true)
				boardOrigin := "http://omo.localhost:8090"
				scheme := "http://"
				port := ":8090"
				if public != "" {
					boardOrigin = public
					scheme = "https://"
					port = ":8443"
				}
				origin := scheme + "dev-" + f.ID + "-user." + base + port
				origin2 := scheme + "dev-" + f2.ID + "-user." + base + port
				h := http.Header{"Authorization": {"Bearer " + s.token}, "Content-Type": {"application/json"}}
				open := subdomainRequest(t, s, "POST", boardOrigin+"/api/proxy/forwards/"+f.ID+"/open", "{}", h)
				wantStatus(t, open.Code, 200)
				var result proxyOpenResult
				json.Unmarshal(open.Body.Bytes(), &result)
				u, err := url.Parse(result.URL)
				if err != nil {
					t.Fatal(err)
				}
				if u.Scheme+"://"+u.Host != origin || u.Path != "/_omo/proxy/bootstrap" || u.Fragment == "" {
					t.Fatalf("Open: %s", u)
				}
				jar, _ := cookiejar.New(nil)
				boardURL, _ := url.Parse(boardOrigin + f.BasePath)
				jar.SetCookies(boardURL, open.Result().Cookies())
				// Authenticated board Open establishes a valid host-local path session.
				pathHeaders := http.Header{}
				for _, c := range jar.Cookies(boardURL) {
					pathHeaders.Add("Cookie", c.Name+"="+c.Value)
				}
				path := subdomainRequest(t, s, "GET", boardOrigin+f.BasePath+"path?q=1", "", pathHeaders)
				wantStatus(t, path.Code, 200)
				if path.Header().Get("X-Path") != f.BasePath+"path?q=1" {
					t.Fatal(path.Header())
				}
				exchangeHeaders := http.Header{"Content-Type": {"application/json"}, "Origin": {origin}}
				// An accepted alias cannot redeem another origin's grant.
				wrong := subdomainRequest(t, s, "POST", boardOrigin+"/_omo/proxy/exchange", `{"grant":"`+u.Fragment+`"}`, http.Header{"Content-Type": {"application/json"}, "Origin": {boardOrigin}})
				wantStatus(t, wrong.Code, 401)
				exchange := subdomainRequest(t, s, "POST", origin+"/_omo/proxy/exchange", `{"grant":"`+u.Fragment+`"}`, exchangeHeaders)
				wantStatus(t, exchange.Code, 200)
				json.Unmarshal(exchange.Body.Bytes(), &result)
				if result.URL != origin+"/start?q=hello%20world" {
					t.Fatal(result.URL)
				}
				appURL, _ := url.Parse(origin + "/start")
				jar.SetCookies(appURL, exchange.Result().Cookies())
				for _, c := range exchange.Result().Cookies() {
					if c.Domain != base || !c.HttpOnly || c.Secure != (public != "") || c.Path != "/" {
						t.Fatalf("cookie: %+v", c)
					}
				}
				for _, app := range []string{origin, origin2} {
					appURL, _ := url.Parse(app + "/a%2fb?x=%2f")
					appHeaders := http.Header{"Authorization": {"Bearer " + s.token}, "X-Forwarded-Proto": {"evil"}, "X-Forwarded-Host": {"evil"}}
					for _, c := range jar.Cookies(appURL) {
						appHeaders.Add("Cookie", c.Name+"="+c.Value)
					}
					appHeaders.Add("Cookie", "app=yes")
					res := subdomainRequest(t, s, "GET", appURL.String(), "", appHeaders)
					wantStatus(t, res.Code, 200)
					if res.Header().Get("X-Path") != "/a%2fb?x=%2f" || res.Header().Get("X-Cookies") != "app=yes" || res.Header().Get("X-Auth") != "" || res.Header().Get("X-Forwarded") != app {
						t.Fatal(res.Header())
					}
				}
				wantStatus(t, subdomainRequest(t, s, "GET", origin+"/api/tickets", "", nil).Code, 401)
				missing := scheme + "dev-" + strings.Repeat("0", 32) + "-user." + base + port
				mh := http.Header{}
				for _, c := range jar.Cookies(appURL) {
					mh.Add("Cookie", c.Name+"="+c.Value)
				}
				res := subdomainRequest(t, s, "GET", missing+"/", "", mh)
				wantStatus(t, res.Code, 404)
				if !strings.Contains(res.Body.String(), "no longer exists") {
					t.Fatal(res.Body.String())
				}
				baseResponse := subdomainRequest(t, s, "GET", scheme+base+port+"/", "", nil)
				wantStatus(t, baseResponse.Code, 200)
				if baseResponse.Body.String() == "app" {
					t.Fatal("base routed to app")
				}
				// Only same-origin exchange is allowed, including behind TLS termination.
				exchangeHeaders.Set("Origin", boardOrigin)
				wantStatus(t, subdomainRequest(t, s, "POST", origin+"/_omo/proxy/exchange", "{}", exchangeHeaders).Code, 403)
				// Register/list use the preferred address, while preserving base_path.
				list := subdomainRequest(t, s, "GET", boardOrigin+"/api/proxy/forwards", "", h)
				var fs api.ProxyForwardList
				json.Unmarshal(list.Body.Bytes(), &fs)
				if len(fs.Forwards) != 2 {
					t.Fatal(list.Body.String())
				}
				for _, v := range fs.Forwards {
					if !strings.Contains(v.Address, "dev-"+v.ID+"-user."+base+port+"/start?q=hello%20world") || v.BasePath != "/proxy/"+v.ID+"/" {
						t.Fatalf("record %+v", v)
					}
				}
			})
		}
	}
}

func TestProxySubdomainBasicNone(t *testing.T) {
	for _, auth := range []string{"basic", "none"} {
		t.Run(auth, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, r.URL.RequestURI()+"|"+r.Header.Get("Authorization"))
			}))
			defer upstream.Close()
			s := newTestServer(t, subdomainConfig, func(c *serverconfig.Config) { c.Auth = auth; c.BasicAuth = "me:secret" })
			f := proxyForward(t, s, upstream.URL, true)
			origin := "http://dev-" + f.ID + "-user.apps.localhost:8090"
			if auth == "basic" {
				wantStatus(t, subdomainRequest(t, s, "GET", origin+"/", "", nil).Code, 401)
			}
			h := http.Header{}
			if auth == "basic" {
				req := httptest.NewRequest("GET", origin, nil)
				req.SetBasicAuth("me", "secret")
				h = req.Header
			}
			res := subdomainRequest(t, s, "GET", origin+"/root?q=1", "", h)
			wantStatus(t, res.Code, 200)
			if res.Body.String() != "/root?q=1|" {
				t.Fatal(res.Body.String())
			}
		})
	}
}

func TestProxySubdomainSameBasePathMode(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.URL.RequestURI()) }))
	defer upstream.Close()
	s := newTestServer(t, subdomainConfig)
	f := proxyForward(t, s, upstream.URL, false)
	boardOrigin := "http://apps.localhost:8090"
	open := subdomainRequest(t, s, "POST", boardOrigin+"/api/proxy/forwards/"+f.ID+"/open", "{}", http.Header{"Authorization": {"Bearer " + s.token}, "Content-Type": {"application/json"}})
	var result proxyOpenResult
	json.Unmarshal(open.Body.Bytes(), &result)
	u, _ := url.Parse(result.URL)
	appOrigin := "http://dev-" + f.ID + "-user.apps.localhost:8090"
	ex := subdomainRequest(t, s, "POST", appOrigin+"/_omo/proxy/exchange", `{"grant":"`+u.Fragment+`"}`, http.Header{"Content-Type": {"application/json"}, "Origin": {appOrigin}})
	wantStatus(t, ex.Code, 200)
	jar, _ := cookiejar.New(nil)
	appURL, _ := url.Parse(appOrigin)
	jar.SetCookies(appURL, ex.Result().Cookies())
	boardURL, _ := url.Parse(boardOrigin)
	h := http.Header{}
	for _, c := range jar.Cookies(boardURL) {
		h.Add("Cookie", c.Name+"="+c.Value)
	}
	res := subdomainRequest(t, s, "GET", boardOrigin+f.BasePath+"start", "", h)
	wantStatus(t, res.Code, 200)
	if res.Body.String() != f.BasePath+"start" {
		t.Fatal(res.Body.String())
	}
}

func TestProxySubdomainSessionCapacity(t *testing.T) {
	s := newTestServer(t, subdomainConfig)
	f := proxyForward(t, s, "http://127.0.0.1:1", false)
	s.proxyAuth.sessions = make(map[string]time.Time)
	for i := range proxyAuthLimit {
		s.proxyAuth.sessions[strconv.Itoa(i)] = time.Now().Add(time.Hour)
	}
	before := len(s.proxyAuth.grants)
	res := subdomainRequest(t, s, "POST", "http://omo.localhost:8090/api/proxy/forwards/"+f.ID+"/open", "{}", http.Header{"Authorization": {"Bearer " + s.token}, "Content-Type": {"application/json"}})
	wantStatus(t, res.Code, 503)
	if len(res.Result().Cookies()) != 0 || len(s.proxyAuth.grants) != before {
		t.Fatal("failed Open retained unissued credentials")
	}
}

func TestProxySubdomainPublicPortOrigin(t *testing.T) {
	s := newTestServer(t, subdomainConfig, func(c *serverconfig.Config) { c.PublicURL = "https://board.example:8443" })
	for _, tc := range []struct{ raw, want string }{
		{"http://dev-0123456789abcdef0123456789abcdef-user.apps.localhost:8443/", "https://dev-0123456789abcdef0123456789abcdef-user.apps.localhost:8443"},
		{"http://dev-0123456789abcdef0123456789abcdef-user.apps.localhost:8090/", "http://dev-0123456789abcdef0123456789abcdef-user.apps.localhost:8090"},
	} {
		req := httptest.NewRequest("GET", tc.raw, nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Host", "evil.example")
		if got := s.proxyOrigin(req); got != tc.want {
			t.Errorf("origin=%s want %s", got, tc.want)
		}
	}
}

func TestProxySubdomainAddressRegistration(t *testing.T) {
	for _, public := range []string{"", "https://board.example:443"} {
		name := "listener"
		if public != "" {
			name = "public-" + strings.ReplaceAll(public, ":", "-")
		}
		t.Run(name, func(t *testing.T) {
			s := newTestServer(t, subdomainConfig, func(c *serverconfig.Config) { c.PublicURL = public })
			origin := "http://omo.localhost:8090"
			wantScheme := "http://"
			port := ":8090"
			if public != "" {
				origin = "https://board.example"
				wantScheme = "https://"
				port = ""
			}
			h := http.Header{"Authorization": {"Bearer " + s.token}, "Content-Type": {"application/json"}}
			res := subdomainRequest(t, s, "POST", origin+"/api/proxy/forwards", `{"name":"registration","port":3000,"start_path":"/start?q=1"}`, h)
			wantStatus(t, res.Code, 201)
			var f api.ProxyForward
			if err := json.Unmarshal(res.Body.Bytes(), &f); err != nil {
				t.Fatal(err)
			}
			if f.Address != wantScheme+"dev-"+f.ID+"-user.apps.localhost"+port+"/start?q=1" || f.BasePath != "/proxy/"+f.ID+"/" {
				t.Fatalf("record: %+v", f)
			}
			// The socket's synthetic host must not become a browser address.
			sock := socketDo(t, s, "GET", "/api/proxy/forwards", nil, "")
			var list api.ProxyForwardList
			if err := json.Unmarshal(sock.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Forwards) != 1 || list.Forwards[0].Address != f.Address {
				t.Fatalf("socket: %s", sock.Body.String())
			}
		})
	}
}

func TestProxyInternalPreservesBoardOriginRules(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) { c.AllowedHosts = []string{"board.example:8090"} })
	h := http.Header{"Origin": {"https://board.example:8090"}}
	res := subdomainRequest(t, s, "GET", "http://board.example:8090/_omo/proxy/bootstrap.js", "", h)
	wantStatus(t, res.Code, 200)
	h.Set("Origin", "https://foreign.example:8090")
	wantStatus(t, subdomainRequest(t, s, "GET", "http://board.example:8090/_omo/proxy/bootstrap.js", "", h).Code, 403)
}

func TestProxySubdomainOptionalFragments(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct{ prefix, suffix, label string }{
		{"", "", id}, {"PRE-", "", "pre-" + id}, {"", "-USER", id + "-user"}, {strings.Repeat("a", 31), "", strings.Repeat("a", 31) + id},
	} {
		c := serverconfig.Defaults()
		c.ProxyBaseHost = "Apps.Localhost"
		c.ProxyLabelPrefix = tc.prefix
		c.ProxyLabelSuffix = tc.suffix
		s := &Server{opts: Options{Config: c}}
		req := httptest.NewRequest("GET", "http://"+tc.label+".apps.localhost:8090/", nil)
		if !s.allowedHost(req) {
			t.Fatal(req.Host)
		}
		got, base := s.proxyHost(req.URL.Hostname())
		if got != id || base {
			t.Fatalf("id=%s base=%v", got, base)
		}
	}
}
