package server

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/proxy"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func TestProxyBrowserWebSocket(t *testing.T) {
	for _, auth := range []string{"token", "basic", "none"} {
		t.Run(auth, func(t *testing.T) {
			target := func(label string) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, Subprotocols: []string{"app"}})
					if err != nil {
						return
					}
					defer c.CloseNow()
					ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					defer cancel()
					kind, data, err := c.Read(ctx)
					if err != nil {
						return
					}
					c.Write(ctx, kind, []byte(label+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization")+" "+r.Header.Get("Cookie")+" "+string(data)))
				}))
			}
			a := target("a")
			t.Cleanup(a.Close)
			upB := target("b")
			t.Cleanup(upB.Close)
			s := newTestServer(t, func(c *serverconfig.Config) { c.Auth = auth; c.BasicAuth = "me:secret" })
			b := newBrowser(t, s)
			f1 := proxyForward(t, s, a.URL, true)
			f2 := proxyForward(t, s, upB.URL, false)
			h := http.Header{}
			if auth == "token" {
				h = proxyLogin(t, s, b, f1)
				h = proxyLogin(t, s, b, f2)
			} else {
				h.Set("Cookie", proxy.ForwardCookie+"="+f2.ID)
				if auth == "basic" {
					req, _ := http.NewRequest("GET", "http://omo", nil)
					req.SetBasicAuth("me", "secret")
					h.Set("Authorization", req.Header.Get("Authorization"))
				}
			}
			if auth != "basic" {
				h.Set("Authorization", "Basic app-credential")
			}
			h.Add("Cookie", "application=present")
			h.Set("Origin", "https://app-client.example")
			dial := func(path string, h http.Header) (*websocket.Conn, *http.Response, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				return websocket.Dial(ctx, "ws"+strings.TrimPrefix(b.srv.URL, "http")+path, &websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{"app", "omo-token." + s.token}})
			}
			for path, label := range map[string]string{"/api/app-ws?x=a%2Fb": "b", f1.BasePath + "socket": "a"} {
				c, res, err := dial(path, h)
				if err != nil {
					t.Fatalf("dial %s: %v (%v)", path, err, res)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err = c.Write(ctx, websocket.MessageText, []byte("ping"))
				if err != nil {
					t.Fatal(err)
				}
				_, data, err := c.Read(ctx)
				c.CloseNow()
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				expectedPath := "/socket"
				if label == "b" {
					expectedPath = strings.TrimSuffix(f2.BasePath, "/") + path
				}
				credential := "Basic app-credential"
				if auth == "basic" {
					credential = ""
				}
				want := label + " " + expectedPath + " " + credential + " application=present ping"
				if string(data) != want {
					t.Fatalf("upstream: %q want %q", data, want)
				}
			}
			if auth != "none" {
				for _, path := range []string{f1.BasePath + "socket", "/api/app-ws"} {
					c, res, err := dial(path, http.Header{"Cookie": {proxy.ForwardCookie + "=" + f1.ID}})
					if c != nil {
						c.CloseNow()
					}
					if err == nil || res == nil || res.StatusCode != 401 {
						t.Fatalf("unauthenticated %s: %v %+v", path, err, res)
					}
				}
			}
			if err := s.board.DeleteProxyForward(context.Background(), board.User, f2.ID); err != nil {
				t.Fatal(err)
			}
			c, res, err := dial("/api/app-ws", h)
			if c != nil {
				c.CloseNow()
			}
			if err == nil || res == nil || res.StatusCode != 404 {
				t.Fatalf("deleted fallback: %v %+v", err, res)
			}
			if auth == "token" {
				// Expired sessions also reject the no-Referer fallback.
				s.proxyAuth.mu.Lock()
				for key := range s.proxyAuth.sessions {
					s.proxyAuth.sessions[key] = time.Now().Add(-time.Second)
				}
				s.proxyAuth.mu.Unlock()
				c, res, err := dial("/api/app-ws", h)
				if c != nil {
					c.CloseNow()
				}
				if err == nil || res == nil || res.StatusCode != 401 {
					t.Fatalf("expired WS accepted: %v", err)
				}
			}
		})
	}
}

func TestProxyBrowserSSEAndBoardEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "public, max-age=10")
		io.WriteString(w, "data: first\n\n")
		http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := proxyForward(t, s, upstream.URL, true)
	h := proxyLogin(t, s, b, f)
	for _, path := range []string{f.BasePath + "events", "/api/events"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", b.srv.URL+path, nil)
		req.Header = h.Clone()
		req.Header.Set("Authorization", "Bearer "+s.token)
		res, err := b.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		wantStatus(t, res.StatusCode, 200)
		line, err := bufio.NewReader(res.Body).ReadString('\n')
		res.Body.Close()
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if path == "/api/events" {
			if line != "retry: 3000\n" || res.Header.Get("Content-Security-Policy") != contentSecurityPolicy {
				t.Fatalf("board SSE: %s %v", line, res.Header)
			}
		} else if line != "data: first\n" || res.Header.Get("Cache-Control") != "public, max-age=10" || res.Header.Get("Content-Security-Policy") != "" {
			t.Fatalf("app SSE: %s %v", line, res.Header)
		}
	}
}
