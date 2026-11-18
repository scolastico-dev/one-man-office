package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/proxy"
)

func TestPluginForwardRequestActivityCancellationAndFailure(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	installTaskPlugin(t, s, "one")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: port, Name: "stream", StripPrefix: true}})
	if err != nil {
		t.Fatal(err)
	}
	h := proxyLogin(t, s, b, f.ProxyForward)
	before, _ := s.ListForwards(t.Context(), "one")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", b.srv.URL+f.BasePath+"stream", nil)
	req.Header = h
	resp, err := b.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 13)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}
	fs, _ := s.ListForwards(t.Context(), "one")
	if fs[0].Activity.ActiveRequests != 1 || fs[0].Activity.ActiveWebSockets != 0 || fs[0].Activity.Revision != before[0].Activity.Revision || fs[0].Activity.NoClientSince != before[0].Activity.NoClientSince {
		t.Fatalf("stream activity %+v", fs[0])
	}
	cancel()
	resp.Body.Close()
	eventually(t, "cancel accounts request", func() bool { fs, _ := s.ListForwards(t.Context(), "one"); return fs[0].Activity.ActiveRequests == 0 })
	upstream.Close()
	res := proxyRequest(t, b, "GET", f.BasePath, "", h)
	wantStatus(t, res.StatusCode, 502)
	eventually(t, "failure accounts request", func() bool { fs, _ := s.ListForwards(t.Context(), "one"); return fs[0].Activity.ActiveRequests == 0 })
	// A failed downstream hijack must release the request without a WS count.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		w.WriteHeader(101)
	}))
	defer target.Close()
	u, _ = url.Parse(target.URL)
	port, _ = strconv.Atoi(u.Port())
	second, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: port, Name: "hijack", StripPrefix: true}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	rq := httptest.NewRequest("GET", "http://127.0.0.1"+second.BasePath, nil)
	rq.Header.Set("Connection", "Upgrade")
	rq.Header.Set("Upgrade", "websocket")
	s.serveProxyForward(rr, rq, second.ProxyForward, proxy.PathMode)
	fs, _ = s.ListForwards(t.Context(), "one")
	for _, row := range fs {
		if row.Activity.ActiveRequests != 0 || row.Activity.ActiveWebSockets != 0 || row.Activity.EverConnected {
			t.Fatalf("failed hijack counted %+v", row)
		}
	}
}
