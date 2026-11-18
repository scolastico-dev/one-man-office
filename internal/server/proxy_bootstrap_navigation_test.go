package server

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

// A real board click may cross sites when Open selects a forward subdomain or
// public_url. Only the inert bootstrap document may bypass fetch-site rejection.
func TestProxyBootstrapCrossSiteNavigation(t *testing.T) {
	for _, mode := range []string{"path", "public-path", "subdomain", "public-subdomain"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestServer(t, func(c *serverconfig.Config) {
				c.Port = 8090
				if strings.Contains(mode, "subdomain") {
					subdomainConfig(c)
				}
				if strings.HasPrefix(mode, "public-") {
					c.PublicURL = "https://board.example:8443"
				}
			})
			f := proxyForward(t, s, "http://127.0.0.1:1", false)
			boardOrigin := "http://127.0.0.1:8090"
			auth := http.Header{"Authorization": {"Bearer " + s.token}, "Content-Type": {"application/json"}}
			openPath := boardOrigin + "/api/proxy/forwards/" + f.ID + "/open"
			open := subdomainRequest(t, s, "POST", openPath, "{}", auth)
			wantStatus(t, open.Code, 200)
			var result proxyOpenResult
			if err := json.Unmarshal(open.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(result.URL)
			if err != nil {
				t.Fatal(err)
			}
			origin := u.Scheme + "://" + u.Host
			bootstrap := origin + "/_omo/proxy/bootstrap"
			s.proxyAuth.mu.Lock()
			grants, sessions := maps.Clone(s.proxyAuth.grants), maps.Clone(s.proxyAuth.sessions)
			s.proxyAuth.mu.Unlock()
			for _, method := range []string{"GET", "HEAD"} {
				t.Run(method, func(t *testing.T) {
					h := http.Header{"Sec-Fetch-Site": {"cross-site"}, "Sec-Fetch-Mode": {"navigate"}, "Sec-Fetch-Dest": {"document"}, "Referer": {boardOrigin + "/"}}
					res := subdomainRequest(t, s, method, bootstrap, "", h)
					wantStatus(t, res.Code, 200)
					if res.Header().Get("Content-Type") != "text/html; charset=utf-8" || res.Header().Get("Content-Security-Policy") != contentSecurityPolicy || res.Header().Get("Cache-Control") != "no-store" || res.Header().Get("Referrer-Policy") != "no-referrer" || len(res.Result().Cookies()) != 0 {
						t.Fatal("bootstrap must remain an inert, uncached document with board security headers")
					}
					if method == "HEAD" && res.Body.Len() != 0 {
						t.Fatal("HEAD sent a body")
					}
				})
			}
			for _, tc := range []struct {
				name, method, path, mode, dest, origin string
			}{
				{"foreign Origin", "GET", "/_omo/proxy/bootstrap", "navigate", "document", "https://evil.example"},
				{"supplied same Origin", "GET", "/_omo/proxy/bootstrap", "navigate", "document", origin},
				{"script fetch", "GET", "/_omo/proxy/bootstrap", "no-cors", "script", ""},
				{"fetch", "GET", "/_omo/proxy/bootstrap", "cors", "empty", ""},
				{"frame", "GET", "/_omo/proxy/bootstrap", "navigate", "iframe", ""},
				{"missing mode", "GET", "/_omo/proxy/bootstrap", "", "document", ""},
				{"missing destination", "GET", "/_omo/proxy/bootstrap", "navigate", "", ""},
				{"POST document", "POST", "/_omo/proxy/bootstrap", "navigate", "document", ""},
				{"script endpoint", "GET", "/_omo/proxy/bootstrap.js", "navigate", "document", ""},
				{"script inclusion", "GET", "/_omo/proxy/bootstrap.js", "no-cors", "script", ""},
				{"trailing slash", "GET", "/_omo/proxy/bootstrap/", "navigate", "document", ""},
				{"encoded path", "GET", "/_omo/proxy/%62ootstrap", "navigate", "document", ""},
				{"exchange", "POST", "/_omo/proxy/exchange", "navigate", "document", ""},
				{"exchange fetch", "POST", "/_omo/proxy/exchange", "cors", "empty", "https://evil.example"},
				{"unknown internal", "GET", "/_omo/proxy/unknown", "navigate", "document", ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					h := http.Header{"Sec-Fetch-Site": {"cross-site"}, "Sec-Fetch-Mode": {tc.mode}, "Sec-Fetch-Dest": {tc.dest}, "Content-Type": {"application/json"}}
					if tc.origin != "" {
						h.Set("Origin", tc.origin)
					}
					res := subdomainRequest(t, s, tc.method, origin+tc.path, `{"grant":"`+u.Fragment+`"}`, h)
					wantStatus(t, res.Code, 403)
					if len(res.Result().Cookies()) != 0 {
						t.Fatal("rejected request set cookies")
					}
				})
			}
			// A foreign Origin also fails without a fetch-site hint.
			wantStatus(t, subdomainRequest(t, s, "GET", bootstrap, "", http.Header{"Origin": {"https://evil.example"}, "Sec-Fetch-Mode": {"navigate"}, "Sec-Fetch-Dest": {"document"}}).Code, 403)
			for _, origins := range [][]string{{""}, {"", "https://evil.example"}} {
				// Supplying an empty first Origin must not hide another value or
				// qualify as a navigation with no Origin header.
				h := http.Header{"Origin": origins, "Sec-Fetch-Site": {"cross-site"}, "Sec-Fetch-Mode": {"navigate"}, "Sec-Fetch-Dest": {"document"}}
				wantStatus(t, subdomainRequest(t, s, "GET", bootstrap, "", h).Code, 403)
			}
			s.proxyAuth.mu.Lock()
			unchanged := reflect.DeepEqual(grants, s.proxyAuth.grants) && reflect.DeepEqual(sessions, s.proxyAuth.sessions)
			s.proxyAuth.mu.Unlock()
			if !unchanged {
				t.Fatal("bootstrap delivery or rejected requests changed grants/sessions")
			}
			// The document exception never reaches board routes, even with auth.
			crossSite := auth.Clone()
			crossSite.Set("Sec-Fetch-Site", "cross-site")
			crossSite.Set("Sec-Fetch-Mode", "navigate")
			crossSite.Set("Sec-Fetch-Dest", "document")
			for _, path := range []string{"/api/server", "/api/proxy/forwards"} {
				wantStatus(t, subdomainRequest(t, s, "GET", boardOrigin+path, "", crossSite).Code, 403)
			}
			wantStatus(t, subdomainRequest(t, s, "POST", openPath, "{}", crossSite).Code, 403)
			wantStatus(t, subdomainRequest(t, s, "GET", "http://evil.example:8090/_omo/proxy/bootstrap", "", crossSite).Code, 403)
			if strings.Contains(mode, "subdomain") {
				badPort := *u
				badPort.Host = u.Hostname() + ":9999"
				badPort.Fragment = ""
				wantStatus(t, subdomainRequest(t, s, "GET", badPort.String(), "", crossSite).Code, 403)
			}
			// Admission/JSON failures do not burn the grant; a normal exchange wins
			// exactly once and creates the cookies only at that boundary.
			body := `{"grant":"` + u.Fragment + `"}`
			exchangeHeaders := http.Header{"Origin": {origin}, "Sec-Fetch-Site": {"same-origin"}, "Content-Type": {"text/plain"}}
			wantStatus(t, subdomainRequest(t, s, "POST", origin+"/_omo/proxy/exchange", body, exchangeHeaders).Code, 400)
			exchangeHeaders.Set("Content-Type", "application/json")
			exchange := subdomainRequest(t, s, "POST", origin+"/_omo/proxy/exchange", body, exchangeHeaders)
			wantStatus(t, exchange.Code, 200)
			if len(exchange.Result().Cookies()) == 0 {
				t.Fatal("exchange did not establish proxy cookies")
			}
			wantStatus(t, subdomainRequest(t, s, "POST", origin+"/_omo/proxy/exchange", body, exchangeHeaders).Code, 401)
			// Removing a forward still refuses Open, grant redemption and routing.
			open = subdomainRequest(t, s, "POST", openPath, "{}", auth)
			wantStatus(t, open.Code, 200)
			if err := json.Unmarshal(open.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			pending, _ := url.Parse(result.URL)
			if err := s.board.DeleteProxyForward(t.Context(), board.User, f.ID); err != nil {
				t.Fatal(err)
			}
			wantStatus(t, subdomainRequest(t, s, "POST", openPath, "{}", auth).Code, 404)
			s.proxyAuth.mu.Lock()
			sessions = maps.Clone(s.proxyAuth.sessions)
			s.proxyAuth.mu.Unlock()
			removed := subdomainRequest(t, s, "POST", origin+"/_omo/proxy/exchange", `{"grant":"`+pending.Fragment+`"}`, exchangeHeaders)
			wantStatus(t, removed.Code, 401)
			if len(removed.Result().Cookies()) != 0 {
				t.Fatal("removed forward established cookies")
			}
			s.proxyAuth.mu.Lock()
			unchanged = reflect.DeepEqual(sessions, s.proxyAuth.sessions)
			s.proxyAuth.mu.Unlock()
			if !unchanged {
				t.Fatal("removed forward established a session")
			}
			routingHeaders := http.Header{}
			for _, c := range exchange.Result().Cookies() {
				routingHeaders.Add("Cookie", c.Name+"="+c.Value)
			}
			appPath := f.BasePath
			if strings.Contains(mode, "subdomain") {
				appPath = "/"
			}
			wantStatus(t, subdomainRequest(t, s, "GET", origin+appPath, "", routingHeaders).Code, 404)
		})
	}
}
