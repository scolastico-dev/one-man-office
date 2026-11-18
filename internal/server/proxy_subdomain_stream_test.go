package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func TestProxySubdomainWebSocketOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Default origin verification must work for an app opening its own WebSocket.
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.Write(r.Context(), websocket.MessageText, []byte(r.URL.RequestURI()+"|"+r.Header.Get("Origin")))
	}))
	defer upstream.Close()
	s := newTestServer(t, subdomainConfig, func(c *serverconfig.Config) { c.Auth = "none" })
	b := newBrowser(t, s)
	f := proxyForward(t, s, upstream.URL, false)
	host := "dev-" + f.ID + "-user.apps.localhost:" + b.port
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, b.srv.Listener.Addr().String())
	}}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, path := range []string{"/socket?q=1", "/api/terminals/app/socket"} {
		c, res, err := websocket.Dial(ctx, "ws://"+host+path, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: http.Header{"Origin": {"http://" + host}}})
		if err != nil {
			t.Fatalf("websocket: %v (%v)", err, res)
		}
		_, data, err := c.Read(ctx)
		c.CloseNow()
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != path+"|"+upstream.URL {
			t.Fatalf("upstream: %s", data)
		}
	}
	// Foreign origins remain subject to upstream's policy.
	c, res, err := websocket.Dial(ctx, "ws://"+host+"/socket", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: http.Header{"Origin": {"https://foreign.example"}}})
	if c != nil {
		c.CloseNow()
	}
	if err == nil || res == nil || res.StatusCode != 403 {
		t.Fatalf("foreign origin: %v %v", err, res)
	}
}
