package server

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func TestPluginForwardIdleAfterReloadDisconnect(t *testing.T) {
	for _, mode := range []string{"path", "subdomain"} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					return
				}
				defer c.Close()
				fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
				if rw.Flush() != nil {
					return
				}
				<-release // Peer retains its write side after the browser has disconnected.
			}))
			defer target.Close()
			s := newTestServer(t, func(c *serverconfig.Config) {
				if mode == "subdomain" {
					c.ProxyBaseHost = "apps.localhost"
				}
			})
			b := newBrowser(t, s)
			installTaskPlugin(t, s, "one")
			cmd, args := taskSleeper(t)
			task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "editor", Command: cmd, Args: args})
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(target.URL)
			port, _ := strconv.Atoi(u.Port())
			f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{TaskID: task.ID, CreateProxyForward: api.CreateProxyForward{Port: port, Name: "tool", StripPrefix: true}})
			if err != nil {
				t.Fatal(err)
			}
			openURL := proxyOpen(t, s, b, f.ProxyForward)
			exchange, _ := http.NewRequest("POST", b.srv.URL+"/_omo/proxy/exchange", strings.NewReader(`{"grant":"`+openURL.Fragment+`"}`))
			exchange.Host = openURL.Host
			exchange.Header = http.Header{"Content-Type": {"application/json"}, "Origin": {openURL.Scheme + "://" + openURL.Host}}
			response, err := b.srv.Client().Do(exchange)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus(t, response.StatusCode, 200)
			response.Body.Close()
			headers := http.Header{}
			for _, cookie := range response.Cookies() {
				headers.Add("Cookie", cookie.Name+"="+cookie.Value)
			}
			activity := func() plugins.ForwardActivity {
				t.Helper()
				fs, err := s.ListForwards(t.Context(), "one")
				if err != nil || len(fs) != 1 || fs[0].ID != f.ID || fs[0].TaskID != task.ID {
					t.Fatalf("forward identity: %+v %v", fs, err)
				}
				return fs[0].Activity
			}
			open := func() net.Conn {
				t.Helper()
				c, err := net.Dial("tcp", strings.TrimPrefix(b.srv.URL, "http://"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close() })
				requestPath, host := f.BasePath+"socket", "127.0.0.1:"+b.port
				if mode == "subdomain" {
					requestPath = "/socket"
					host = f.ID + ".apps.localhost:" + b.port
				}
				req, _ := http.NewRequest("GET", "http://"+host+requestPath, nil)
				req.Header = headers.Clone()
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
				if err := req.Write(c); err != nil {
					t.Fatal(err)
				}
				res, err := http.ReadResponse(bufio.NewReader(c), req)
				if err != nil {
					t.Fatal(err)
				}
				if res.StatusCode != 101 {
					t.Fatalf("upgrade %d", res.StatusCode)
				}
				return c
			}
			first, reloaded := open(), open()
			if a := activity(); a.ActiveWebSockets != 2 {
				t.Fatalf("two sockets: %+v", a)
			}
			if err := s.WatchForward(t.Context(), "one", f.ID, .1); err != nil {
				t.Fatal(err)
			}
			first.Close()
			for activity().ActiveWebSockets != 1 {

				time.Sleep(time.Millisecond)
			}
			if a := activity(); a.NoClientSince != "" {
				t.Fatalf("remaining socket was marked idle: %+v", a)
			}
			// Keep the replacement connected beyond the selected idle interval.
			time.Sleep(150 * time.Millisecond)
			tasks, _ := s.plugins.m.Tasks("one")
			if len(tasks) != 1 || tasks[0].ID != task.ID || tasks[0].State != "running" {
				t.Fatalf("live replacement stopped: %+v", tasks)
			}
			reloaded.Close()
			eventually(t, "last browser EOF stops stable task with idle reason", func() bool {
				tasks, _ := s.plugins.m.Tasks("one")
				return len(tasks) == 1 && tasks[0].ID == task.ID && tasks[0].State == "exited" && tasks[0].Reason == "idle"
			})
			if fs, err := s.ListForwards(t.Context(), "one"); err != nil || len(fs) != 0 {
				t.Fatalf("idle forward retained: %+v %v", fs, err)
			}
		})
	}
}
