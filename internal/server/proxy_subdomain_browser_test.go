package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

// Opt-in standalone Chromium fixture. It serves only in-memory httptest
// handlers and temporary homes; no omo binary, socket listener, CLI, engine
// loop or agents are started.
func TestProxySubdomainBrowserFixture(t *testing.T) {
	if os.Getenv("PROXY_BROWSER_FIXTURE") != "1" {
		t.Skip("run node --test web/scripts/proxy-subdomain.test.mjs")
	}
	done := make(chan struct{})
	var once sync.Once
	fixtures := []map[string]string{}
	for _, variant := range []string{"http", "https", "same-base", "same-domain-board"} {
		var upstream *httptest.Server
		upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/inspect"):
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"path": r.URL.RequestURI(), "cookie": r.Header.Get("Cookie"), "authorization": r.Header.Get("Authorization"), "host": r.Header.Get("X-Forwarded-Host")})
			case r.URL.Path == "/redirect":
				w.Header().Set("Location", upstream.URL+"/final?q=1")
				w.Header().Add("Set-Cookie", "app=yes; Domain=127.0.0.1; Path=/; SameSite=Lax")
				w.WriteHeader(302)
			default:
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, "<!doctype html><h1>Application</h1>")
			}
		}))
		t.Cleanup(upstream.Close)
		s := newTestServer(t, subdomainConfig, func(c *serverconfig.Config) { c.AllowedHosts = []string{"board.localhost"} })
		f := proxyForward(t, s, upstream.URL, false)
		f2 := proxyForward(t, s, upstream.URL, false)
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/__fixture_stop" {
				once.Do(func() { close(done) })
				w.WriteHeader(204)
				return
			}
			s.BrowserHandler().ServeHTTP(w, r)
		}))
		scheme := "http"
		if variant == "https" {
			server.StartTLS()
			scheme = "https"
		} else {
			server.Start()
		}
		t.Cleanup(server.Close)
		_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
		boardHost := "board.localhost"
		if variant == "same-base" {
			boardHost = "apps.localhost"
		}
		if variant == "same-domain-board" {
			boardHost = "board.apps.localhost"
			s.opts.Config.AllowedHosts = append(s.opts.Config.AllowedHosts, boardHost)
		}
		boardURL := scheme + "://" + boardHost + ":" + port
		if variant == "https" {
			s.opts.Config.PublicURL = boardURL
		}
		fixtures = append(fixtures, map[string]string{"variant": variant, "board": boardURL, "forward": scheme + "://dev-" + f.ID + "-user.apps.localhost:" + port, "forward2": scheme + "://dev-" + f2.ID + "-user.apps.localhost:" + port, "base": scheme + "://apps.localhost:" + port, "id": f.ID, "base_path": f.BasePath, "token": s.token, "stop": server.URL + "/__fixture_stop"})
	}
	data, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("PROXY_BROWSER_FIXTURES %s\n", data)
	<-done
}
