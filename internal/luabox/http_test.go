package luabox

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// httpBox opens a box with omo.http on client and loads src, which must
// define f; it returns f's result.
func httpCall(t *testing.T, h *HTTP, lim Limits, src string) (any, *Box, error) {
	t.Helper()
	b := New(context.Background(), lim)
	t.Cleanup(b.Close)
	b.Set("http", h.Func(b))
	if err := b.Load(src, "http"); err != nil {
		return nil, b, err
	}
	v, err := b.Call("f")
	return v, b, err
}

// get returns a script whose f requests url and returns
// {status, body, err}.
func get(url string) string {
	return `function f() local r, e = omo.http({url = "` + url + `"}) if r then return {status = r.status, body = r.body} end return {err = e} end`
}

// noDial is a transport that fails the test when a request is sent.
type noDial struct {
	t    *testing.T
	sent atomic.Int32
}

func (n *noDial) RoundTrip(*http.Request) (*http.Response, error) {
	n.sent.Add(1)
	return nil, errors.New("dialed")
}

func TestHTTPReach(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("a cookie was sent")
		}
		http.SetCookie(w, &http.Cookie{Name: "c", Value: "v"})
		_, _ = w.Write([]byte("tls " + r.Header.Get("Authorization")))
	}))
	defer tlsSrv.Close()
	v, _, err := httpCall(t, &HTTP{Client: tlsSrv.Client()}, Limits{}, `function f()
		local r1 = omo.http({url = "`+tlsSrv.URL+`", headers = {Authorization = "Bearer abcdefgh"}})
		local r2 = omo.http({url = "`+tlsSrv.URL+`"})
		return r1.body .. "|" .. r2.body end`)
	if err != nil || v != "tls Bearer abcdefgh|tls " {
		t.Fatalf("https: %v %v", v, err)
	}

	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain"))
	}))
	defer plain.Close()
	v, _, err = httpCall(t, &HTTP{}, Limits{}, get(plain.URL))
	if err != nil || v.(map[string]any)["body"] != "plain" {
		t.Fatalf("http to 127.0.0.1: %v %v", v, err)
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(plain.URL, "http://"))
	v, _, err = httpCall(t, &HTTP{}, Limits{}, get("http://localhost:"+port))
	if err != nil || v.(map[string]any)["body"] != "plain" {
		t.Fatalf("http to localhost: %v %v", v, err)
	}

	nd := &noDial{t: t}
	for _, u := range []string{
		"http://example.invalid/", "http://10.0.0.1/", "http://127.1/", "http://localhost.example.com/",
		"ftp://example.com/", "file:///etc/passwd", "https:///nohost", "//example.com/", "gopher://127.0.0.1/",
	} {
		v, _, err := httpCall(t, &HTTP{Client: &http.Client{Transport: nd}}, Limits{}, get(u))
		if err != nil || v.(map[string]any)["err"] != phraseHTTPReach {
			t.Errorf("%s: %v %v", u, v, err)
		}
	}
	if nd.sent.Load() != 0 {
		t.Fatalf("%d refused requests were sent", nd.sent.Load())
	}

	// Redirects: same host and port only.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the redirect to another host was followed")
	}))
	defer other.Close()
	_, otherPort, _ := net.SplitHostPort(strings.TrimPrefix(other.URL, "http://"))
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/done", http.StatusFound)
		case "/done":
			_, _ = w.Write([]byte("done"))
		case "/port":
			http.Redirect(w, r, "http://127.0.0.1:"+otherPort+"/", http.StatusFound)
		case "/host":
			http.Redirect(w, r, "http://localhost:"+otherPort+"/", http.StatusFound)
		case "/far":
			http.Redirect(w, r, "https://example.invalid/", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	}))
	defer redir.Close()
	v, _, _ = httpCall(t, &HTTP{}, Limits{}, get(redir.URL+"/same"))
	if v.(map[string]any)["body"] != "done" {
		t.Fatalf("same-host redirect: %v", v)
	}
	for _, path := range []string{"/port", "/host", "/far", "/loop"} {
		v, _, _ := httpCall(t, &HTTP{}, Limits{}, get(redir.URL+path))
		if v.(map[string]any)["err"] != phraseHTTPRedirect {
			t.Errorf("%s: %v", path, v)
		}
	}
}

func TestHTTPBounds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		path := r.URL.Path
		if strings.HasSuffix(path, "/slow") {
			path = "/slow"
		}
		switch path {
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", MaxHTTPBody+1)))
		case "/max":
			_, _ = w.Write([]byte(strings.Repeat("x", MaxHTTPBody)))
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()
	secretURL := srv.URL + "/path-with-secret-q"

	v, b, err := httpCall(t, &HTTP{}, Limits{}, `function f()
		local out = {}
		for i = 1, 9 do local r, e = omo.http({url = "`+secretURL+`"}) out[i] = r and "ok" or e end
		return out end`)
	if err != nil {
		t.Fatal(err)
	}
	list := v.([]any)
	if list[7] != "ok" || list[8] != phraseHTTPTooMany || hits.Load() != 8 {
		t.Fatalf("requests: %v, hits %d", list, hits.Load())
	}
	_ = b

	v, _, _ = httpCall(t, &HTTP{MaxRequests: 1}, Limits{}, get(srv.URL+"/big"))
	if v.(map[string]any)["err"] != phraseHTTPTooLarge {
		t.Fatalf("big body: %v", v)
	}
	v, _, _ = httpCall(t, &HTTP{}, Limits{}, `function f() local r = omo.http({url = "`+srv.URL+`/max"}) return #r.body end`)
	if v != float64(MaxHTTPBody) {
		t.Fatalf("body of exactly 1 MiB: %v", v)
	}
	v, _, _ = httpCall(t, &HTTP{}, Limits{}, `function f() local r, e = omo.http({url = "`+srv.URL+`", body = ("x"):rep(1048576) .. "x"}) return e end`)
	if v != phraseHTTPBodyLarge {
		t.Fatalf("big request: %v", v)
	}

	start := time.Now()
	v, _, _ = httpCall(t, &HTTP{}, Limits{}, `function f() local r, e = omo.http({url = "`+secretURL+`/slow", timeout = 0.2}) return e end`)
	if v != phraseHTTPTimeout || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout: %v after %v", v, time.Since(start))
	}
	// The run's deadline ends a request whose own timeout is longer.
	start = time.Now()
	_, _, err = httpCall(t, &HTTP{}, Limits{Timeout: 300 * time.Millisecond}, `function f() local r, e = omo.http({url = "`+srv.URL+`/slow", timeout = 30}) return e end`)
	if err == nil || err.Error() != msgTimeLimit || time.Since(start) > 3*time.Second {
		t.Fatalf("deadline: %v after %v", err, time.Since(start))
	}

	// Parsing refusals are fixed phrases and never hold the URL.
	for _, req := range []string{
		`{url = "` + secretURL + `", bogus = 1}`,
		`{url = "` + secretURL + `", method = "GET /x"}`,
		`{url = "` + secretURL + `", headers = {["Bad Name"] = "v"}}`,
		`{url = "` + secretURL + `", headers = {A = "v\r\nInjected: 1"}}`,
		`{url = "` + secretURL + `", json = {}, body = "x"}`,
		`{url = "` + secretURL + `", timeout = "soon"}`,
		`{url = "` + secretURL + `", timeout = -1}`,
		`{url = 42}`,
		`"` + secretURL + `"`,
	} {
		v, _, err := httpCall(t, &HTTP{}, Limits{}, `function f() local r, e = omo.http(`+req+`) return e end`)
		e, _ := v.(string)
		if err != nil || e == "" || strings.Contains(e, "secret") || strings.Contains(e, "127.0.0.1") {
			t.Errorf("%s: %q %v", req, e, err)
		}
	}
	// A failed connection names no address.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	v, _, _ = httpCall(t, &HTTP{}, Limits{}, get("http://"+addr+"/secret-path"))
	if v.(map[string]any)["err"] != phraseHTTPFailed {
		t.Fatalf("refused connection: %v", v)
	}
}

func TestHTTPSendsBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(strings.Builder)
		_, _ = (func() (int64, error) {
			b := make([]byte, 1024)
			n, _ := r.Body.Read(b)
			buf.Write(b[:n])
			return 0, nil
		})()
		_, _ = w.Write([]byte(r.Method + " " + r.Header.Get("Content-Type") + " " + buf.String()))
	}))
	defer srv.Close()
	v, _, err := httpCall(t, &HTTP{}, Limits{}, `function f()
		local a = omo.http({url = "`+srv.URL+`", method = "post", json = {k = "v"}}).body
		local b = omo.http({url = "`+srv.URL+`", method = "POST", form = {k = "v w"}}).body
		return a .. "|" .. b end`)
	if err != nil || v != `POST application/json {"k":"v"}|POST application/x-www-form-urlencoded k=v+w` {
		t.Fatalf("%v %v", v, err)
	}
}

func TestHTTPGoroutinesEndAfterTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	for i := 0; i < 3; i++ {
		b := New(context.Background(), Limits{Timeout: 100 * time.Millisecond})
		b.Set("http", (&HTTP{}).Func(b))
		_ = b.Load(`omo.http({url = "`+srv.URL+`"})`, "x")
		b.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for boxGoroutines() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := boxGoroutines(); n != 0 {
		t.Fatalf("%d goroutines left", n)
	}
}
