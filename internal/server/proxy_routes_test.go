package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func proxyForward(t *testing.T, s *Server, target string, strip bool) api.ProxyForward {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	f, err := s.board.CreateProxyForward(context.Background(), board.User, api.CreateProxyForward{Host: u.Hostname(), Port: port, Name: "demo", StartPath: "/start?q=hello%20world", StripPrefix: strip})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func proxyRequest(t *testing.T, b *browser, method, path, body string, h http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, b.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = h.Clone()
	client := *b.srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func proxyOpen(t *testing.T, s *Server, b *browser, f api.ProxyForward) *url.URL {
	t.Helper()
	res := proxyRequest(t, b, "POST", "/api/proxy/forwards/"+f.ID+"/open", "{}", http.Header{"Authorization": {"Bearer " + s.token}, "Content-Type": {"application/json"}})
	wantStatus(t, res.StatusCode, 200)
	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func proxyLogin(t *testing.T, s *Server, b *browser, f api.ProxyForward) http.Header {
	t.Helper()
	u := proxyOpen(t, s, b, f)
	res := proxyRequest(t, b, "POST", "/_omo/proxy/exchange", `{"grant":"`+u.Fragment+`"}`, http.Header{"Content-Type": {"application/json"}, "Origin": {b.srv.URL}})
	wantStatus(t, res.StatusCode, 200)
	h := http.Header{}
	for _, c := range res.Cookies() {
		h.Add("Cookie", c.Name+"="+c.Value)
	}
	return h
}

func TestProxyOpenAndExchange(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, "http://127.0.0.1:1", false)
	path := "/api/proxy/forwards/" + f.ID + "/open"
	wantStatus(t, proxyRequest(t, b, "POST", path, "{}", http.Header{"Content-Type": {"application/json"}}).StatusCode, 401)
	wantStatus(t, socketDo(t, s, "POST", path, map[string]any{}, liveSession(t, s, api.RoleDeveloper).ID).Code, 403)
	u := proxyOpen(t, s, b, f)
	if u.Scheme+"://"+u.Host != b.srv.URL || u.Path != "/_omo/proxy/bootstrap" || u.RawQuery != "" || u.Fragment == "" || strings.Contains(u.String(), s.token) {
		t.Fatalf("open URL: %s", u)
	}
	page := proxyRequest(t, b, "GET", u.Path, "", nil)
	wantStatus(t, page.StatusCode, 200)
	bytes, _ := io.ReadAll(page.Body)
	if !strings.Contains(string(bytes), `src="/_omo/proxy/bootstrap.js"`) || strings.Contains(string(bytes), u.Fragment) || strings.Contains(string(bytes), s.token) || page.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("bootstrap: %s %v", bytes, page.Header)
	}
	script := proxyRequest(t, b, "GET", "/_omo/proxy/bootstrap.js", "", nil)
	wantStatus(t, script.StatusCode, 200)
	h := http.Header{"Content-Type": {"application/json"}, "Origin": {b.srv.URL}}
	body := `{"grant":"` + u.Fragment + `"}`
	wantStatus(t, proxyRequest(t, b, "POST", "/_omo/proxy/exchange", `{"grant":"tampered"}`, h).StatusCode, 401)
	res := proxyRequest(t, b, "POST", "/_omo/proxy/exchange", body, h)
	wantStatus(t, res.StatusCode, 200)
	var dest struct {
		URL string `json:"url"`
	}
	json.NewDecoder(res.Body).Decode(&dest)
	if dest.URL != b.srv.URL+f.Address {
		t.Fatalf("destination %s", dest.URL)
	}
	cookies := res.Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies %+v", cookies)
	}
	for _, c := range cookies {
		if c.Path != "/" || !c.HttpOnly || c.Domain != "" || c.SameSite != http.SameSiteLaxMode || c.Secure || c.MaxAge != int(proxySessionTTL.Seconds()) {
			t.Fatalf("cookie %+v", c)
		}
	}
	wantStatus(t, proxyRequest(t, b, "POST", "/_omo/proxy/exchange", body, h).StatusCode, 401)
	// Return addresses are server-owned; the exchange accepts no client override.
	u = proxyOpen(t, s, b, f)
	wantStatus(t, proxyRequest(t, b, "POST", "/_omo/proxy/exchange", `{"grant":"`+u.Fragment+`","url":"https://evil.example"}`, h).StatusCode, 400)
	wantStatus(t, proxyRequest(t, b, "POST", "/_omo/proxy/exchange", body, http.Header{"Content-Type": {"text/plain"}}).StatusCode, 400)
	u = proxyOpen(t, s, b, f)
	sessionsBefore := len(s.proxyAuth.sessions)
	if err := s.board.DeleteProxyForward(context.Background(), board.User, f.ID); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, proxyRequest(t, b, "POST", "/_omo/proxy/exchange", `{"grant":"`+u.Fragment+`"}`, h).StatusCode, 401)
	if len(s.proxyAuth.sessions) != sessionsBefore {
		t.Fatal("failed exchange retained an orphan session")
	}
}

func TestProxyOpenPublicURL(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) { c.PublicURL = "https://office.example:8443" })
	b := newBrowser(t, s)
	f := proxyForward(t, s, "http://127.0.0.1:1", false)
	u := proxyOpen(t, s, b, f)
	if u.Scheme != "https" || u.Host != "office.example:8443" {
		t.Fatal(u)
	}
	// Exchange must occur on the intended destination, not an accepted alias.
	h := http.Header{"Content-Type": {"application/json"}}
	body := `{"grant":"` + u.Fragment + `"}`
	wantStatus(t, proxyRequest(t, b, "POST", "/_omo/proxy/exchange", body, h).StatusCode, 401)
	req := newRequest(t, "POST", u.Scheme+"://"+u.Host+"/_omo/proxy/exchange", map[string]string{"grant": u.Fragment})
	req.Header.Set("Origin", "https://office.example:8443")
	rec := httptest.NewRecorder()
	s.BrowserHandler().ServeHTTP(rec, req)
	wantStatus(t, rec.Code, 200)
	for _, c := range rec.Result().Cookies() {
		if !c.Secure {
			t.Fatal("public https cookie not Secure")
		}
	}
}

func TestProxyTokenRouting(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("X-App-Authorization", r.Header.Get("Authorization"))
		w.Header().Set("X-App-Cookie", r.Header.Get("Cookie"))
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		data, _ := io.ReadAll(r.Body)
		w.Write([]byte(r.Method + " " + r.URL.RequestURI() + " " + string(data)))
	}))
	defer upstream.Close()
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, upstream.URL, true)
	ref := b.srv.URL + f.BasePath + "start"
	for _, path := range []string{f.BasePath, "/api/app?x=1", "/assets/app.js"} {
		for _, cookie := range []string{"", "omo_proxy_session=forged"} {
			res := proxyRequest(t, b, "GET", path, "", http.Header{"Referer": {ref}, "Cookie": {cookie}})
			wantStatus(t, res.StatusCode, 401)
			data, _ := io.ReadAll(res.Body)
			if !strings.Contains(string(data), "Back to board") {
				t.Fatalf("missing board link: %s", data)
			}
		}
	}
	h := proxyLogin(t, s, b, f)
	h.Set("Authorization", "Bearer app-secret")
	h.Add("Cookie", "application=present")
	h.Set("Origin", "https://app-client.example")
	h.Set("Sec-Fetch-Site", "cross-site")
	res := proxyRequest(t, b, "POST", f.BasePath+"upload?q=a%2Fb", "hello", h)
	wantStatus(t, res.StatusCode, 200)
	data, _ := io.ReadAll(res.Body)
	if string(data) != "POST /upload?q=a%2Fb hello" || res.Header.Get("Cache-Control") != "public, max-age=60" || res.Header.Get("Content-Security-Policy") != "default-src 'self'" || res.Header.Get("Access-Control-Allow-Origin") != "https://app-client.example" {
		t.Fatalf("app: %s %v", data, res.Header)
	}
	if res.Header.Get("X-App-Authorization") != "Bearer app-secret" || res.Header.Get("X-App-Cookie") != "application=present" {
		t.Fatalf("credentials: %v", res.Header)
	}
	h.Set("Referer", ref)
	res = proxyRequest(t, b, "POST", "/api/upload?x=a%2Fb", "payload", h)
	wantStatus(t, res.StatusCode, 307)
	if res.Header.Get("Location") != f.BasePath+"api/upload?x=a%2Fb" {
		t.Fatal(res.Header)
	}
	res = proxyRequest(t, b, "POST", res.Header.Get("Location"), "payload", h)
	wantStatus(t, res.StatusCode, 200)
	data, _ = io.ReadAll(res.Body)
	if string(data) != "POST /api/upload?x=a%2Fb payload" {
		t.Fatal(string(data))
	}
	// A valid board token is removed on proxy requests but cannot replace a session.
	h.Del("Origin")
	h.Del("Sec-Fetch-Site")
	h.Set("Authorization", "Bearer "+s.token)
	res = proxyRequest(t, b, "GET", f.BasePath, "", h)
	wantStatus(t, res.StatusCode, 200)
	if res.Header.Get("X-App-Authorization") != "" {
		t.Fatal("board token leaked")
	}
	wantStatus(t, proxyRequest(t, b, "GET", f.BasePath, "", http.Header{"Authorization": {"Bearer " + s.token}}).StatusCode, 401)
	// Non-proxy Referers cannot hijack ordinary board routing.
	for _, ref := range []string{"https://evil.example" + f.BasePath, "http://evil@" + strings.TrimPrefix(b.srv.URL, "http://") + f.BasePath, b.srv.URL + ".evil.example" + f.BasePath, "https://" + strings.TrimPrefix(b.srv.URL, "http://") + f.BasePath, b.srv.URL + strings.TrimSuffix(f.BasePath, "/")} {
		res = proxyRequest(t, b, "GET", "/api/server", "", http.Header{"Referer": {ref}, "Authorization": {"Bearer " + s.token}})
		wantStatus(t, res.StatusCode, 200)
		if res.Header.Get("Content-Security-Policy") != contentSecurityPolicy {
			t.Fatal("spoofed ref routed to app")
		}
	}
	// Expiry and deletion are checked on every explicit/fallback request.
	s.proxyAuth.mu.Lock()
	for key := range s.proxyAuth.sessions {
		s.proxyAuth.sessions[key] = time.Now().Add(-time.Second)
	}
	s.proxyAuth.mu.Unlock()
	wantStatus(t, proxyRequest(t, b, "GET", f.BasePath, "", h).StatusCode, 401)
	h = proxyLogin(t, s, b, f)
	h.Set("Referer", b.srv.URL+f.BasePath)
	if err := s.board.DeleteProxyForward(context.Background(), board.User, f.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{f.BasePath, "/api/app"} {
		wantStatus(t, proxyRequest(t, b, "GET", path, "", h).StatusCode, 404)
	}
}

func TestProxyBasicAndNone(t *testing.T) {
	for _, auth := range []string{"basic", "none"} {
		for _, strip := range []bool{false, true} {
			t.Run(auth+strconv.FormatBool(strip), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-App-Authorization", r.Header.Get("Authorization"))
					w.Write([]byte(r.URL.RequestURI()))
				}))
				defer upstream.Close()
				s := newTestServer(t, func(c *serverconfig.Config) { c.Auth = auth; c.BasicAuth = "me:secret" })
				b := newBrowser(t, s)
				f := proxyForward(t, s, upstream.URL, strip)
				h := http.Header{}
				if auth == "basic" {
					for _, path := range []string{f.BasePath, "/api/root"} {
						res := proxyRequest(t, b, "GET", path, "", http.Header{"Referer": {b.srv.URL + f.BasePath}})
						wantStatus(t, res.StatusCode, 401)
						if res.Header.Get("WWW-Authenticate") == "" {
							t.Fatal("no basic challenge")
						}
					}
					req, _ := http.NewRequest("GET", "http://omo", nil)
					req.SetBasicAuth("me", "secret")
					h.Set("Authorization", req.Header.Get("Authorization"))
				}
				h.Set("Content-Type", "application/json")
				res := proxyRequest(t, b, "POST", "/api/proxy/forwards/"+f.ID+"/open", "{}", h)
				wantStatus(t, res.StatusCode, 200)
				dest := decodeBody[proxyOpenResult](t, res.Body)
				if dest.URL != b.srv.URL+f.Address || strings.Contains(dest.URL, "#") {
					t.Fatal(dest)
				}
				res = proxyRequest(t, b, "GET", f.BasePath+"page?q=1", "", h)
				wantStatus(t, res.StatusCode, 200)
				body, _ := io.ReadAll(res.Body)
				want := f.BasePath + "page?q=1"
				if strip {
					want = "/page?q=1"
				}
				if string(body) != want {
					t.Fatal(string(body))
				}
				if auth == "basic" && res.Header.Get("X-App-Authorization") != "" {
					t.Fatal("omo basic credentials leaked")
				}
				if auth == "none" {
					h.Set("Authorization", "Basic app-credential")
					res = proxyRequest(t, b, "GET", f.BasePath, "", h)
					if res.Header.Get("X-App-Authorization") != "Basic app-credential" {
						t.Fatal("app basic auth stripped")
					}
				}
				h.Set("Referer", b.srv.URL+f.BasePath)
				res = proxyRequest(t, b, "POST", "/api/app?keep=1", "form=data", h)
				wantStatus(t, res.StatusCode, 307)
				if res.Header.Get("Location") != f.BasePath+"api/app?keep=1" {
					t.Fatal(res.Header)
				}
			})
		}
	}
}

func TestProxyReservedEndpointsAndBoard(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, "http://127.0.0.1:1", false)
	h := proxyLogin(t, s, b, f)
	h.Set("Referer", b.srv.URL+f.BasePath)
	for _, path := range []string{"/_omo/proxy/bootstrap", "/_omo/proxy/bootstrap.js", "/_omo/proxy/unknown"} {
		res := proxyRequest(t, b, "GET", path, "", h)
		if res.StatusCode == 307 {
			t.Fatal("internal route hijacked")
		}
	}
	h.Del("Referer")
	// Proxy cookies never authenticate the board API.
	wantStatus(t, proxyRequest(t, b, "GET", "/api/server", "", h).StatusCode, 401)
	h.Set("Authorization", "Bearer "+s.token)
	res := proxyRequest(t, b, "GET", "/api/server", "", h)
	wantStatus(t, res.StatusCode, 200)
	if res.Header.Get("Content-Security-Policy") != contentSecurityPolicy || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(res.Header)
	}
	res = proxyRequest(t, b, "GET", "/", "", h)
	wantStatus(t, res.StatusCode, 200)
	if res.Header.Get("Content-Security-Policy") != contentSecurityPolicy {
		t.Fatal(res.Header)
	}
	wantStatus(t, proxyRequest(t, b, "POST", "/api/server/paused", "not json", h).StatusCode, 400)
	h.Set("Origin", "https://evil.example")
	wantStatus(t, proxyRequest(t, b, "GET", "/api/server", "", h).StatusCode, 403)
	wantStatus(t, proxyRequest(t, b, "POST", "/_omo/proxy/exchange", `{}`, h).StatusCode, 403)
	h.Del("Origin")
	// Exact board sockets remain on the board despite a last-forward cookie.
	h.Set("Upgrade", "websocket")
	h.Set("Connection", "Upgrade")
	for path, want := range map[string]int{"/api/agents/missing/terminal": 503, "/api/terminals/missing/socket": 404} {
		wantStatus(t, proxyRequest(t, b, "GET", path, "", h).StatusCode, want)
		// They remain board sockets even if a client sends a proxy Referer.
		h.Set("Referer", b.srv.URL+f.BasePath)
		wantStatus(t, proxyRequest(t, b, "GET", path, "", h).StatusCode, want)
		h.Del("Referer")
	}
	// Missing slash adds only the slash and retains the exact query.
	h.Del("Upgrade")
	h.Del("Connection")
	res = proxyRequest(t, b, "GET", strings.TrimSuffix(f.BasePath, "/")+"?x=a%2Fb", "", h)
	wantStatus(t, res.StatusCode, 307)
	if res.Header.Get("Location") != f.BasePath+"?x=a%2Fb" {
		t.Fatal(res.Header)
	}
}

func TestProxyForwardingPublicContext(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Actual-Forwarded", r.Header.Get("X-Forwarded-Proto")+"://"+r.Header.Get("X-Forwarded-Host"))
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	s := newTestServer(t, func(c *serverconfig.Config) { c.Auth = "none"; c.PublicURL = "https://office.example:8443" })
	f := proxyForward(t, s, upstream.URL, false)
	req := httptest.NewRequest("GET", "http://office.example:8443"+f.BasePath, nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	rec := httptest.NewRecorder()
	s.BrowserHandler().ServeHTTP(rec, req)
	wantStatus(t, rec.Code, 204)
	if rec.Header().Get("X-Actual-Forwarded") != "https://office.example:8443" {
		t.Fatal(rec.Header())
	}
	for _, c := range rec.Result().Cookies() {
		if !c.Secure {
			t.Fatal("insecure proxy cookie")
		}
	}
}

func TestProxyOpenRefusesClientReturnPaths(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, "http://127.0.0.1:1", false)
	h := http.Header{"Content-Type": {"application/json"}, "Authorization": {"Bearer " + s.token}}
	for _, body := range []string{`{"return_path":"//evil.example"}`, `{"url":"https://evil.example"}`, `{"start_path":"/../api"}`} {
		res := proxyRequest(t, b, "POST", "/api/proxy/forwards/"+f.ID+"/open", body, h)
		wantStatus(t, res.StatusCode, 400)
	}
}

func TestProxyFallbackRedirectReplaysPost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		w.Write([]byte(r.Method + " " + r.URL.RequestURI() + " " + string(data)))
	}))
	defer upstream.Close()
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, upstream.URL, true)
	h := proxyLogin(t, s, b, f)
	h.Set("Referer", b.srv.URL+f.BasePath+"page")
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	req, _ := http.NewRequest("POST", b.srv.URL+"/api/app?x=a%2Fb", strings.NewReader("payload=with+body"))
	req.Header = h
	res, err := b.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	wantStatus(t, res.StatusCode, 200)
	data, _ := io.ReadAll(res.Body)
	if string(data) != "POST /api/app?x=a%2Fb payload=with+body" {
		t.Fatal(string(data))
	}
}

func TestProxyCookieAndGrantFailures(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, "http://127.0.0.1:1", false)
	u := proxyOpen(t, s, b, f)
	s.proxyAuth.mu.Lock()
	grant := s.proxyAuth.grants[u.Fragment]
	grant.expires = time.Now().Add(-time.Second)
	s.proxyAuth.grants[u.Fragment] = grant
	s.proxyAuth.mu.Unlock()
	wantStatus(t, proxyRequest(t, b, "POST", "/_omo/proxy/exchange", `{"grant":"`+u.Fragment+`"}`, http.Header{"Content-Type": {"application/json"}}).StatusCode, 401)
	h := proxyLogin(t, s, b, f)
	h.Add("Cookie", "omo_proxy_session=shadow")
	h.Set("Referer", b.srv.URL+f.BasePath)
	for _, path := range []string{f.BasePath, "/api/root"} {
		wantStatus(t, proxyRequest(t, b, "GET", path, "", h).StatusCode, 401)
	}
	h = proxyLogin(t, s, b, f)
	// Reaching a dead target keeps the record and links to the canonical board.
	res := proxyRequest(t, b, "GET", f.BasePath, "", h)
	wantStatus(t, res.StatusCode, 502)
	data, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(data), "Back to the board") || strings.Contains(string(data), s.token) {
		t.Fatalf("unavailable: %s", data)
	}
	if _, err := s.board.LiveProxyForward(context.Background(), board.System, f.ID); err != nil {
		t.Fatal(err)
	}
	h.Set("Referer", b.srv.URL+f.BasePath)
	s.proxyAuth.mu.Lock()
	for key := range s.proxyAuth.sessions {
		s.proxyAuth.sessions[key] = time.Now().Add(-time.Second)
	}
	s.proxyAuth.mu.Unlock()
	wantStatus(t, proxyRequest(t, b, "POST", "/api/app?x=1", "body", h).StatusCode, 401)
}

func TestProxyGrantOriginDefaultPort(t *testing.T) {
	for _, public := range []string{"https://office.example:443/", "http://office.example:80/"} {
		t.Run(public, func(t *testing.T) {
			s := newTestServer(t, func(c *serverconfig.Config) { c.PublicURL = public })
			b := newBrowser(t, s)
			f := proxyForward(t, s, "http://127.0.0.1:1", false)
			u := proxyOpen(t, s, b, f)
			// Browsers remove default ports and lowercase host names during URL parsing.
			req := newRequest(t, "POST", u.Scheme+"://OFFICE.example/_omo/proxy/exchange", map[string]string{"grant": u.Fragment})
			req.Header.Set("Origin", u.Scheme+"://office.example")
			rec := httptest.NewRecorder()
			s.BrowserHandler().ServeHTTP(rec, req)
			wantStatus(t, rec.Code, 200)
		})
	}
}

func TestProxyOpenUsesRemoteBrowserOrigin(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, "http://127.0.0.1:1", false)
	// Run advertises omo.localhost on a wildcard listener. A remote browser
	// needs its accepted address, not that loopback-only printed hint.
	s.mu.Lock()
	s.address = "http://omo.localhost:8090/"
	s.mu.Unlock()
	u := proxyOpen(t, s, b, f)
	if u.Scheme+"://"+u.Host != b.srv.URL {
		t.Fatalf("Open switched browser host: %s", u)
	}
}

func TestProxyDoesNotLeakDuplicateOmoAuthorization(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Join(r.Header.Values("Authorization"), "|")))
	}))
	defer upstream.Close()
	s := newTestServer(t, func(c *serverconfig.Config) { c.BasicAuth = "me:secret" })
	b := newBrowser(t, s)
	f := proxyForward(t, s, upstream.URL, true)
	basic, _ := http.NewRequest("GET", "http://omo", nil)
	basic.SetBasicAuth("me", "secret")
	for _, credential := range []string{"Bearer " + s.token, basic.Header.Get("Authorization")} {
		h := proxyLogin(t, s, b, f)
		h.Add("Authorization", "Bearer application")
		h.Add("Authorization", credential)
		res := proxyRequest(t, b, "GET", f.BasePath, "", h)
		wantStatus(t, res.StatusCode, 200)
		data, _ := io.ReadAll(res.Body)
		if string(data) != "" {
			t.Fatal("omo credential survived duplicate Authorization filtering")
		}
	}
}
