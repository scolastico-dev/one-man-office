package server

import (
	"bytes"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func stubApp(t *testing.T, files fstest.MapFS) {
	t.Helper()
	old := appFiles
	appFiles = func() (fs.FS, bool) {
		if files == nil {
			return nil, false
		}
		return files, true
	}
	t.Cleanup(func() { appFiles = old })
}

func builtApp(t *testing.T) {
	t.Helper()
	stubApp(t, fstest.MapFS{
		"index.html":                 {Data: []byte(`<!doctype html><div id="app"></div>`)},
		"assets/index-AbCd1234.js":   {Data: []byte("x")},
		"assets/index-AbCd1234.css":  {Data: []byte("a{}")},
		"assets/font-AbCd1234.woff2": {Data: []byte("w")},
		"assets/logo-AbCd1234.svg":   {Data: []byte("<svg/>")},
		"assets/logo-AbCd1234.png":   {Data: []byte("p")},
		"assets/data-AbCd1234.json":  {Data: []byte("{}")},
		"assets/OFL-AbCd1234.txt":    {Data: []byte("licence")},
		"assets/nested/deep-1234.js": {Data: []byte("d")},
		"secret.txt":                 {Data: []byte("outside assets")},
	})
}

func TestAppIndexAtRootAndTicketPaths(t *testing.T) {
	builtApp(t)
	b := newBrowser(t, newTestServer(t))
	for _, p := range []string{"/", "/123", "/123456", "/some/deep/link", "/secret.txt"} {
		for _, method := range []string{"GET", "HEAD"} {
			resp := b.do(method, p, nil, nil)
			wantStatus(t, resp.StatusCode, http.StatusOK)
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Fatalf("%s %s: content type %q", method, p, ct)
			}
			if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
				t.Fatalf("%s %s: cache control %q", method, p, cc)
			}
			body, _ := io.ReadAll(resp.Body)
			if method == "GET" && !strings.Contains(string(body), `id="app"`) {
				t.Fatalf("%s: not the app page: %s", p, body)
			}
		}
	}
	wantStatus(t, b.do("POST", "/123", nil, nil).StatusCode, http.StatusMethodNotAllowed)
}

func TestAppAssetsAreImmutable(t *testing.T) {
	builtApp(t)
	b := newBrowser(t, newTestServer(t))
	types := map[string]string{
		"/assets/index-AbCd1234.js":   "text/javascript",
		"/assets/index-AbCd1234.css":  "text/css",
		"/assets/font-AbCd1234.woff2": "font/woff2",
		"/assets/logo-AbCd1234.svg":   "image/svg+xml",
		"/assets/logo-AbCd1234.png":   "image/png",
		"/assets/data-AbCd1234.json":  "application/json",
		"/assets/OFL-AbCd1234.txt":    "text/plain",
		"/assets/nested/deep-1234.js": "text/javascript",
	}
	for p, want := range types {
		for _, method := range []string{"GET", "HEAD"} {
			resp := b.do(method, p, nil, nil)
			wantStatus(t, resp.StatusCode, http.StatusOK)
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, want) {
				t.Fatalf("%s %s: content type %q, want %q", method, p, ct, want)
			}
			if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
				t.Fatalf("%s %s: cache control %q", method, p, cc)
			}
			if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("%s %s: nosniff missing", method, p)
			}
			if resp.Header.Get("Content-Security-Policy") == "" {
				t.Fatalf("%s %s: CSP missing", method, p)
			}
		}
	}
}

func TestAppMissingAssetIs404(t *testing.T) {
	builtApp(t)
	b := newBrowser(t, newTestServer(t))
	for _, p := range []string{"/assets/nope.js", "/assets/", "/assets/nested/", "/assets/sub/../index.html", "/assets/index.html", "/assets/..%2fsecret.txt", "/assets/%2e%2e/secret.txt", "/assets/%2e%2e%2findex.html"} {
		resp := b.do("GET", p, nil, nil)
		wantStatus(t, resp.StatusCode, http.StatusNotFound)
		body, _ := io.ReadAll(resp.Body)
		if bytes.Contains(body, []byte(`id="app"`)) || bytes.Contains(body, []byte("outside assets")) {
			t.Fatalf("%s: served a page or outside file: %s", p, body)
		}
	}
}

// A cleaned path that leaves /assets/ is the app page for that address, never
// a file from the embedded folder.
func TestAppTraversalNeverServesFiles(t *testing.T) {
	builtApp(t)
	b := newBrowser(t, newTestServer(t))
	for _, p := range []string{"/assets/../secret.txt", "/assets/%2e%2e/secret.txt", "/assets/nested/../../secret.txt"} {
		resp := b.do("GET", p, nil, nil)
		body, _ := io.ReadAll(resp.Body)
		if bytes.Contains(body, []byte("outside assets")) {
			t.Fatalf("%s: served a file outside assets", p)
		}
	}
}

func TestAppPageNeedsNoToken(t *testing.T) {
	builtApp(t)
	b := newBrowser(t, newTestServer(t))
	wantStatus(t, b.do("GET", "/", nil, nil).StatusCode, http.StatusOK)
	wantStatus(t, b.do("GET", "/123", nil, nil).StatusCode, http.StatusOK)
	wantStatus(t, b.do("GET", "/assets/index-AbCd1234.js", nil, nil).StatusCode, http.StatusOK)
	wantStatus(t, b.do("GET", "/api/server", nil, nil).StatusCode, http.StatusUnauthorized)
	wantStatus(t, b.do("GET", "/api/tickets", nil, nil).StatusCode, http.StatusUnauthorized)
}

func TestAppWithBasicAuthNeedsCredentials(t *testing.T) {
	builtApp(t)
	b := newBrowser(t, newTestServer(t, func(c *serverconfig.Config) {
		c.Auth = "basic"
		c.BasicAuth = "me:secret"
	}))
	for _, p := range []string{"/", "/123", "/assets/index-AbCd1234.js", "/api/tickets"} {
		wantStatus(t, b.do("GET", p, nil, nil).StatusCode, http.StatusUnauthorized)
	}
	hdr := map[string]string{"Authorization": "Basic bWU6c2VjcmV0"}
	wantStatus(t, b.do("GET", "/123", nil, hdr).StatusCode, http.StatusOK)
	wantStatus(t, b.do("GET", "/assets/index-AbCd1234.js", nil, hdr).StatusCode, http.StatusOK)
}

func TestAppRejectsForeignHost(t *testing.T) {
	builtApp(t)
	b := newBrowser(t, newTestServer(t))
	for _, p := range []string{"/", "/assets/index-AbCd1234.js"} {
		wantStatus(t, b.do("GET", p, nil, map[string]string{"Host": "evil.example"}).StatusCode, http.StatusForbidden)
	}
}

func TestAppNotBuiltServesNotice(t *testing.T) {
	stubApp(t, nil)
	b := newBrowser(t, newTestServer(t))
	for _, p := range []string{"/", "/123", "/some/link"} {
		resp := b.do("GET", p, nil, nil)
		wantStatus(t, resp.StatusCode, http.StatusOK)
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: content type %q", p, ct)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "make web") {
			t.Fatalf("%s: no notice: %s", p, body)
		}
	}
	wantStatus(t, b.do("GET", "/assets/x.js", nil, nil).StatusCode, http.StatusNotFound)
}

func TestWarningWhenUINotBuilt(t *testing.T) {
	const line = "the board is not built into this binary"
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
	for _, built := range []bool{false, true} {
		if built {
			builtApp(t)
		} else {
			stubApp(t, nil)
		}
		var out bytes.Buffer
		printWarnings(&out, serverconfig.Defaults(), addr)
		if got := strings.Contains(out.String(), line); got == built {
			t.Fatalf("built=%v: warning present=%v: %q", built, got, out.String())
		}
	}
}

func TestAPINotFoundStaysJSON(t *testing.T) {
	builtApp(t)
	s := newTestServer(t)
	resp := newBrowser(t, s).do("GET", "/api/nope", nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusNotFound)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q", ct)
	}
	if code := errorCode(t, resp.Body); code != "not_found" {
		t.Fatalf("code %q", code)
	}
}
