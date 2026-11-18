package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/proxy"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

// Keep the navigation/cache invariant across every generated proxy HTML writer:
// transport failures, proxyPage (denial/missing/exchange), and bootstrap HTML.
func TestProxyGeneratedPages(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	target := upstream.URL
	upstream.Close()
	for _, auth := range []string{"token", "basic", "none"} {
		for _, subdomain := range []bool{false, true} {
			mode := "path"
			if subdomain {
				mode = "subdomain"
			}
			t.Run(auth+"/"+mode, func(t *testing.T) {
				s := newTestServer(t, subdomainConfig, func(c *serverconfig.Config) {
					c.Auth = auth
					c.BasicAuth = "user:secret"
				})
				f := proxyForward(t, s, target, false)
				host, path, board := "omo.localhost:8090", f.BasePath, "http://omo.localhost:8090/"
				missing := "/proxy/" + strings.Repeat("f", 32) + "/"
				missingHost := host
				if subdomain {
					host = "dev-" + f.ID + "-user.apps.localhost:8090"
					path, board = "/", "http://apps.localhost:8090/"
					missingHost = "dev-" + strings.Repeat("f", 32) + "-user.apps.localhost:8090"
					missing = "/"
				}
				session, err := s.proxyAuth.newSession(time.Now())
				if err != nil {
					t.Fatal(err)
				}
				for _, tc := range []struct {
					name, method, host, path string
					status                   int
					unauthenticated          bool
				}{
					{"unavailable", "GET", host, path, 502, false},
					{"missing", "GET", missingHost, missing, 404, false},
					{"bootstrap", "GET", host, "/_omo/proxy/bootstrap", 200, false},
					{"unknown internal", "GET", host, "/_omo/proxy/unknown", 404, false},
					{"failed exchange", "POST", host, "/_omo/proxy/exchange", 401, false},
					{"denied", "GET", host, path, 401, true},
				} {
					if tc.unauthenticated && auth == "none" {
						continue
					}
					t.Run(tc.name, func(t *testing.T) {
						r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, strings.NewReader(`{"grant":"expired"}`))
						r.Header.Set("Content-Type", "application/json")
						r.Header.Set("Origin", "http://"+tc.host)
						if !tc.unauthenticated {
							switch auth {
							case "basic":
								r.SetBasicAuth("user", "secret")
							case "token":
								r.AddCookie(&http.Cookie{Name: proxy.SessionCookie, Value: session})
							}
						}
						w := httptest.NewRecorder()
						s.BrowserHandler().ServeHTTP(w, r)
						if w.Code != tc.status || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
							t.Fatalf("response = %d %v %s", w.Code, w.Header(), w.Body.String())
						}
						for name, want := range map[string]string{"Referrer-Policy": "no-referrer", "Cache-Control": "no-store"} {
							if got := w.Header().Get(name); got != want {
								t.Errorf("%s = %q, want %q", name, got, want)
							}
						}
						if !strings.Contains(w.Body.String(), `href="`+board+`"`) {
							t.Errorf("missing board link %q: %s", board, w.Body.String())
						}
						if auth == "basic" && tc.unauthenticated && w.Header().Get("WWW-Authenticate") == "" {
							t.Error("missing Basic challenge")
						}
					})
				}
			})
		}
	}
}
