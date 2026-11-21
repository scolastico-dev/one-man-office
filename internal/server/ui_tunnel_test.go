package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

const tunnelProtocol = "ui.forwards.tunnel.v1"

func tunnelHeaders(s *Server) http.Header {
	h := http.Header{}
	switch s.opts.Config.Auth {
	case "basic":
		r, _ := http.NewRequest("GET", "http://board", nil)
		user, pass, _ := strings.Cut(s.opts.Config.BasicAuth, ":")
		r.SetBasicAuth(user, pass)
		h = r.Header
	case "none":
	default:
		h.Set("Authorization", "Bearer "+s.token)
	}
	return h
}
func dialTunnel(t *testing.T, s *Server, b *browser, id string) net.Conn {
	t.Helper()
	c, res, err := websocket.Dial(t.Context(), b.srv.URL+"/api/ui/forwards/"+id+"/tunnel", &websocket.DialOptions{HTTPHeader: tunnelHeaders(s), Subprotocols: []string{tunnelProtocol}})
	if err != nil {
		t.Fatalf("tunnel handshake: %v (%v)", err, res)
	}
	t.Cleanup(func() { c.CloseNow() })
	if c.Subprotocol() != tunnelProtocol {
		t.Fatalf("protocol: %q", c.Subprotocol())
	}
	return websocket.NetConn(t.Context(), c, websocket.MessageBinary)
}
func tunnelRequest(t *testing.T, c net.Conn, reader *bufio.Reader, raw string) *http.Response {
	t.Helper()
	if _, err := io.WriteString(c, raw); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// Losing the root mode or filtering would leak board credentials or route to a prefix.
func TestUIForwardTunnelHTTP(t *testing.T) {
	for _, mode := range []string{"token", "basic", "none"} {
		for _, strip := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/strip=%v", mode, strip), func(t *testing.T) {
				s := newTestServer(t, func(c *serverconfig.Config) { c.Auth = mode; c.BasicAuth = "person:password" })
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-Omo-Session") != "" || r.Header.Get("Omo-Secret") != "" || r.Header.Get("X-Forwarded-Prefix") != "" {
						t.Error("reserved headers reached app")
					}
					if got := r.Header.Get("Cookie"); got != "application=yes" {
						t.Errorf("cookies: %q", got)
					}
					w.Header().Set("Location", "http://"+r.Host+"/next?q=a%2Fb")
					w.Header().Add("Set-Cookie", "app=yes; Domain=127.0.0.1; Path=/")
					w.Header().Add("Set-Cookie", "omo_proxy_session=bad; Path=/")
					body, _ := io.ReadAll(r.Body)
					fmt.Fprintf(w, "%s %s %s", r.URL.RequestURI(), r.Header.Get("Authorization"), body)
				}))
				defer upstream.Close()
				b := newBrowser(t, s)
				f := proxyForward(t, s, upstream.URL, strip)
				c := dialTunnel(t, s, b, f.ID)
				reader := bufio.NewReader(c)
				for _, auth := range []string{tunnelHeaders(s).Get("Authorization"), "Bearer app-credential"} {
					raw := "POST /api/echo?x=a%2Fb&__omo_proxy_grant=secret HTTP/1.1\r\nHost: demo.omo\r\nCookie: omo_proxy_session=secret; omo_proxy_forward=secret; application=yes\r\nX-Omo-Session: secret\r\nOmo-Secret: secret\r\nContent-Length: 4\r\n"
					if auth != "" {
						raw += "Authorization: " + auth + "\r\n"
					}
					res := tunnelRequest(t, c, reader, raw+"\r\ndata")
					data, err := io.ReadAll(res.Body)
					if err != nil {
						t.Fatal(err)
					}
					wantAuth := ""
					if auth == "Bearer app-credential" {
						wantAuth = auth
					}
					if res.StatusCode != 200 || string(data) != "/api/echo?x=a%2Fb "+wantAuth+" data" {
						t.Fatalf("response: %d %q", res.StatusCode, data)
					}
					if res.Header.Get("Location") != "http://demo.omo/next?q=a%2Fb" || res.Header.Get("Set-Cookie") != "app=yes; Path=/" || len(res.Header.Values("Set-Cookie")) != 1 {
						t.Fatalf("rewrites: %v", res.Header)
					}
					if res.Header.Get("Content-Security-Policy") != "" {
						t.Fatal("board CSP leaked")
					}
				}
				if err := s.board.DeleteProxyForward(t.Context(), board.User, f.ID); err != nil {
					t.Fatal(err)
				}
				res := tunnelRequest(t, c, reader, "GET / HTTP/1.1\r\nHost: demo.omo\r\n\r\n")
				if res.StatusCode != 404 {
					t.Fatalf("removed forward: %d", res.StatusCode)
				}
			})
		}
	}
}

func TestUIForwardTunnelAccess(t *testing.T) {
	for _, mode := range []string{"token", "basic", "none"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestServer(t, func(c *serverconfig.Config) { c.Auth = mode; c.BasicAuth = "person:password" })
			upstream := httptest.NewServer(http.NotFoundHandler())
			defer upstream.Close()
			b := newBrowser(t, s)
			f := proxyForward(t, s, upstream.URL, false)
			path := "/api/ui/forwards/" + f.ID + "/tunnel"
			for _, tc := range []struct {
				key, value string
				status     int
			}{
				{"Host", "evil.example", 403}, {"Origin", "tauri://localhost", 403}, {"Sec-Fetch-Site", "cross-site", 403},
				{"Sec-WebSocket-Protocol", "unknown", 400},
			} {
				h := tunnelHeaders(s)
				h.Set(tc.key, tc.value)
				protocols := []string{tunnelProtocol}
				if tc.key == "Sec-WebSocket-Protocol" {
					protocols = []string{tc.value}
					h.Del(tc.key)
				}
				c, res, err := websocket.Dial(t.Context(), b.srv.URL+path, &websocket.DialOptions{HTTPHeader: h, Host: h.Get("Host"), Subprotocols: protocols})
				if c != nil {
					c.CloseNow()
				}
				if err == nil || res == nil || res.StatusCode != tc.status {
					t.Fatalf("%s: %v %v", tc.key, err, res)
				}
			}
			if mode != "none" {
				c, res, err := websocket.Dial(t.Context(), b.srv.URL+path, &websocket.DialOptions{Subprotocols: []string{tunnelProtocol}})
				if c != nil {
					c.CloseNow()
				}
				if err == nil || res == nil || res.StatusCode != 401 {
					t.Fatalf("no auth: %v %v", err, res)
				}
			}
			for _, role := range []api.Role{api.RoleCEO, api.RolePM, api.RoleDeveloper, api.RoleReviewer} {
				session := liveSession(t, s, role)
				s.agent(t, session.ID).fails(403, api.CodeForbidden, "GET", path, nil)
			}
			c, res, err := websocket.Dial(t.Context(), b.srv.URL+"/api/ui/forwards/00000000000000000000000000000000/tunnel", &websocket.DialOptions{HTTPHeader: tunnelHeaders(s), Subprotocols: []string{tunnelProtocol}})
			if c != nil {
				c.CloseNow()
			}
			if err == nil || res == nil || res.StatusCode != 404 {
				t.Fatalf("missing: %v %v", err, res)
			}
			// Board-owned socket routing must win over the browser's last-forward fallback.
			h := tunnelHeaders(s)
			h.Set("Cookie", "omo_proxy_forward="+f.ID)
			c, res, err = websocket.Dial(t.Context(), b.srv.URL+path, &websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{tunnelProtocol}})
			if err != nil {
				t.Fatalf("fallback captured tunnel: %v %v", err, res)
			}
			c.CloseNow()
		})
	}
}

func TestUIForwardTunnelStreamingAndFailure(t *testing.T) {
	finished := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, upstream.URL, true)
	c := dialTunnel(t, s, b, f.ID)
	res := tunnelRequest(t, c, bufio.NewReader(c), "GET /events HTTP/1.1\r\nHost: demo.omo\r\n\r\n")
	data := make([]byte, 13)
	if _, err := io.ReadFull(res.Body, data); err != nil || string(data) != "data: first\n\n" {
		t.Fatalf("stream: %q %v", data, err)
	}
	c.Close()
	<-finished
	upstream.Close()
	c = dialTunnel(t, s, b, f.ID)
	res = tunnelRequest(t, c, bufio.NewReader(c), "GET / HTTP/1.1\r\nHost: demo.omo\r\n\r\n")
	data, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != 502 || !strings.Contains(string(data), "Web UI unreachable") || !strings.Contains(string(data), `href="`+b.srv.URL+`/"`) {
		t.Fatalf("failure: %d %q %v", res.StatusCode, data, err)
	}
}

func TestUIForwardTunnelApplicationWebSocketActivity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"app"}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(1 << 20)
		kind, data, err := c.Read(r.Context())
		if err == nil {
			c.Write(r.Context(), kind, data)
		}
		// Keep the app alive until the tunnel client closes.
		c.Read(r.Context())
	}))
	defer upstream.Close()
	s := newTestServer(t)
	b := newBrowser(t, s)
	installTaskPlugin(t, s, "one")
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: port, Name: "app"}})
	if err != nil {
		t.Fatal(err)
	}
	outer := dialTunnel(t, s, b, f.ID)
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return outer, nil }}
	defer transport.CloseIdleConnections()
	c, res, err := websocket.Dial(t.Context(), "ws://demo.omo/socket", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: http.Header{"Origin": {"http://demo.omo"}}, Subprotocols: []string{"app", "omo-token.secret"}})
	if err != nil {
		t.Fatalf("nested WS: %v %v", err, res)
	}
	defer c.CloseNow()
	data := strings.Repeat("large frame", 20000)
	c.SetReadLimit(int64(len(data) + 1))
	if err := c.Write(t.Context(), websocket.MessageBinary, []byte(data)); err != nil {
		t.Fatal(err)
	}
	_, got, err := c.Read(t.Context())
	if err != nil || string(got) != data {
		t.Fatalf("echo bytes=%d: %v", len(got), err)
	}
	fs, err := s.ListForwards(t.Context(), "one")
	if err != nil || len(fs) != 1 || fs[0].Activity.ActiveWebSockets != 1 || !fs[0].Activity.EverConnected {
		t.Fatalf("activity: %+v %v", fs, err)
	}
	c.CloseNow()
	eventually(t, "tunnel socket activity released", func() bool {
		fs, _ := s.ListForwards(t.Context(), "one")
		return fs[0].Activity.ActiveRequests == 0 && fs[0].Activity.ActiveWebSockets == 0
	})
}

func TestUIForwardTunnelSharedFixture(t *testing.T) {
	var fixture struct{ Protocol, Request, ResponseBody string }
	data, err := os.ReadFile("../api/testdata/forward_tunnel_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s %s", r.URL.RequestURI(), body)
	}))
	defer upstream.Close()
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, upstream.URL, false)
	c := dialTunnel(t, s, b, f.ID)
	if fixture.Protocol != tunnelProtocol {
		t.Fatal("fixture protocol mismatch")
	}
	// Message boundaries may bisect a header, body or protocol line.
	for _, part := range []string{fixture.Request[:7], fixture.Request[7:]} {
		if _, err := io.WriteString(c, part); err != nil {
			t.Fatal(err)
		}
	}
	res, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != 200 || string(body) != fixture.ResponseBody {
		t.Fatalf("fixture response: %d %q %v", res.StatusCode, body, err)
	}
}

func TestUIForwardTunnelTLSAndRequestTargets(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "root "+r.URL.RequestURI()) }))
	defer upstream.Close()
	s := newTestServer(t)
	b := newBrowser(t, s)
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	for _, verify := range []bool{false, true} {
		f, err := s.board.CreateProxyForward(t.Context(), board.User, api.CreateProxyForward{Host: u.Hostname(), Port: port, Scheme: "https", Name: "tls", VerifyTLS: verify})
		if err != nil {
			t.Fatal(err)
		}
		c := dialTunnel(t, s, b, f.ID)
		reader := bufio.NewReader(c)
		res := tunnelRequest(t, c, reader, "GET /root HTTP/1.1\r\nHost: demo.omo\r\n\r\n")
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if verify {
			if res.StatusCode != 502 {
				t.Fatalf("untrusted certificate: %d", res.StatusCode)
			}
		} else if res.StatusCode != 200 || string(data) != "root /root" {
			t.Fatalf("trusted local TLS: %d %q", res.StatusCode, data)
		}
		for _, raw := range []string{"CONNECT arbitrary.example:443 HTTP/1.1\r\nHost: arbitrary.example\r\n\r\n", "GET http://arbitrary.example/ HTTP/1.1\r\nHost: arbitrary.example\r\n\r\n"} {
			requestConn := dialTunnel(t, s, b, f.ID)
			res := tunnelRequest(t, requestConn, bufio.NewReader(requestConn), raw)
			io.Copy(io.Discard, res.Body)
			if res.StatusCode != 400 {
				t.Fatalf("request target: %d", res.StatusCode)
			}
		}
	}
}

func TestUIForwardTunnelRejectsInvalidMessagesAndStopsWithServer(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	f := proxyForward(t, s, upstream.URL, false)
	for _, kind := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		ws, _, err := websocket.Dial(t.Context(), b.srv.URL+"/api/ui/forwards/"+f.ID+"/tunnel", &websocket.DialOptions{HTTPHeader: tunnelHeaders(s), Subprotocols: []string{tunnelProtocol}})
		if err != nil {
			t.Fatal(err)
		}
		data := []byte("GET / HTTP/1.1\r\nHost: demo.omo\r\n\r\n")
		if kind == websocket.MessageBinary {
			data = []byte(strings.Repeat("x", 65537))
		}
		_ = ws.Write(t.Context(), kind, data)
		_, _, err = ws.Read(t.Context())
		ws.CloseNow()
		if err == nil {
			t.Fatal("invalid message accepted")
		}
	}
	c := dialTunnel(t, s, b, f.ID)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("tunnel survived server shutdown")
	}
}

// A final HTTP response must drain before inner connection close ends the WS.
func TestUIForwardTunnelConnectionCloseResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "final response") }))
	defer upstream.Close()
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, upstream.URL, false)
	c := dialTunnel(t, s, b, f.ID)
	res := tunnelRequest(t, c, bufio.NewReader(c), "GET / HTTP/1.1\r\nHost: demo.omo\r\nConnection: close\r\n\r\n")
	data, err := io.ReadAll(res.Body)
	if err != nil || string(data) != "final response" {
		t.Fatalf("final response %q %v", data, err)
	}
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("HTTP close must end the stream cleanly: %v", err)
	}
}

func TestUIForwardTunnelCloseDelimitedResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "first ")
		w.(http.Flusher).Flush()
		io.WriteString(w, "last")
	}))
	defer upstream.Close()
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, upstream.URL, false)
	c := dialTunnel(t, s, b, f.ID)
	res := tunnelRequest(t, c, bufio.NewReader(c), "GET / HTTP/1.0\r\nHost: demo.omo\r\n\r\n")
	data, err := io.ReadAll(res.Body)
	if err != nil || string(data) != "first last" {
		t.Fatalf("close-delimited response: %q %v", data, err)
	}
}

func TestUIForwardTunnelClosingForwardBoardLink(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	installTaskPlugin(t, s, "one")
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: port, Name: "closing"}})
	if err != nil {
		t.Fatal(err)
	}
	s.pluginForwards.Lock()
	s.pluginForwards.entries[f.ID].closing = true
	s.pluginForwards.Unlock()
	c := dialTunnel(t, s, b, f.ID)
	res := tunnelRequest(t, c, bufio.NewReader(c), "GET / HTTP/1.1\r\nHost: demo.omo\r\n\r\n")
	data, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != 404 || !strings.Contains(string(data), `href="`+b.srv.URL+`/"`) {
		t.Fatalf("closing forward: %d %q %v", res.StatusCode, data, err)
	}
}
