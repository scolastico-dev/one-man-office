package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func TestPluginForwardWebSocketActivity(t *testing.T) {
	for _, mode := range []string{"path", "subdomain"} {
		t.Run(mode, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/refuse") {
					http.Error(w, "refused", 403)
					return
				}
				if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
					w.WriteHeader(204)
					return
				}
				c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
				if err != nil {
					return
				}
				defer c.CloseNow()
				for {
					kind, data, err := c.Read(r.Context())
					if err != nil {
						return
					}
					if c.Write(r.Context(), kind, data) != nil {
						return
					}
				}
			}))
			defer target.Close()
			s := newTestServer(t, func(c *serverconfig.Config) {
				if mode == "subdomain" {
					c.ProxyBaseHost = "apps.localhost"
				}
			})
			b := newBrowser(t, s)
			installTaskPlugin(t, s, "one")
			u, _ := url.Parse(target.URL)
			port, _ := strconv.Atoi(u.Port())
			cmd, args := taskSleeper(t)
			task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "live", Command: cmd, Args: args})
			if err != nil {
				t.Fatal(err)
			}
			f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{TaskID: task.ID, CreateProxyForward: api.CreateProxyForward{Port: port, Name: "tool", StripPrefix: true}})
			if err != nil {
				t.Fatal(err)
			}
			activity := func() plugins.ForwardActivity {
				t.Helper()
				fs, err := s.ListForwards(t.Context(), "one")
				if err != nil || len(fs) != 1 {
					t.Fatalf("list %+v %v", fs, err)
				}
				return fs[0].Activity
			}
			initial := activity()
			if initial.EverConnected || initial.ActiveWebSockets != 0 || initial.NoClientSince == "" {
				t.Fatalf("never connected %+v", initial)
			}
			uopen := proxyOpen(t, s, b, f.ProxyForward)
			origin := uopen.Scheme + "://" + uopen.Host
			exchangeReq, _ := http.NewRequest("POST", b.srv.URL+"/_omo/proxy/exchange", strings.NewReader(`{"grant":"`+uopen.Fragment+`"}`))
			exchangeReq.Host = uopen.Host
			exchangeReq.Header = http.Header{"Content-Type": {"application/json"}, "Origin": {origin}}
			exchange, err := b.srv.Client().Do(exchangeReq)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus(t, exchange.StatusCode, 200)
			exchange.Body.Close()
			headers := http.Header{}
			for _, cookie := range exchange.Cookies() {
				headers.Add("Cookie", cookie.Name+"="+cookie.Value)
			}
			path := f.BasePath + "socket"
			host := ""
			if mode == "subdomain" {
				path = "/socket"
				host = s.opts.Config.ProxyLabelPrefix + f.ID + s.opts.Config.ProxyLabelSuffix + ".apps.localhost:" + b.port
			}
			dial := func(h http.Header) (*websocket.Conn, *http.Response, error) {
				return websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(b.srv.URL, "http")+path, &websocket.DialOptions{HTTPHeader: h, Host: host})
			}
			c, res, err := dial(nil)
			if c != nil {
				c.CloseNow()
			}
			if err == nil || res == nil || res.StatusCode != 401 {
				t.Fatalf("auth %+v %v", res, err)
			}
			if got := activity(); got != initial {
				t.Fatalf("auth refusal changed activity: %+v", got)
			}
			open := func() *websocket.Conn {
				t.Helper()
				c, _, err := dial(headers)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.CloseNow() })
				return c
			}
			wait := func(n int) plugins.ForwardActivity {
				t.Helper()
				deadline := time.Now().Add(3 * time.Second)
				for {
					a := activity()
					if a.ActiveWebSockets == n && a.ActiveRequests == n {
						return a
					}
					if time.Now().After(deadline) {
						t.Fatalf("activity %+v want %d", a, n)
					}
					time.Sleep(time.Millisecond)
				}
			}
			// An authenticated upstream refusal never constitutes a browser connection.
			refusedPath := strings.TrimSuffix(path, "socket") + "refuse"
			refused, resp, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(b.srv.URL, "http")+refusedPath, &websocket.DialOptions{HTTPHeader: headers, Host: host})
			if refused != nil {
				refused.CloseNow()
			}
			if err == nil || resp == nil || resp.StatusCode != 403 {
				t.Fatalf("upstream refusal %v %+v", err, resp)
			}
			if got := activity(); got != initial {
				t.Fatalf("refusal counted %+v", got)
			}
			a, bconn := open(), open()
			active := wait(2)
			if err := s.WatchForward(t.Context(), "one", f.ID, .02); err != nil {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
			tasks, _ := s.plugins.m.Tasks("one")
			if tasks[0].State != "running" {
				t.Fatal("live WebSockets lost idle deadline")
			}
			if err := s.WatchForward(t.Context(), "one", f.ID, 3600); err != nil {
				t.Fatal(err)
			}
			if !active.EverConnected || active.NoClientSince != "" || active.Revision <= initial.Revision {
				t.Fatalf("active %+v", active)
			}
			a.CloseNow()
			wait(1)
			bconn.CloseNow()
			idle := wait(0)
			if idle.NoClientSince == "" || idle.Revision <= active.Revision || idle.NoClientSince == initial.NoClientSince {
				t.Fatalf("disconnect %+v", idle)
			}
			// Ordinary authenticated health requests cannot extend the WS idle interval.
			req, _ := http.NewRequest("GET", b.srv.URL+path, nil)
			req.Header = headers.Clone()
			req.Host = host
			resp, err = http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if got := activity(); got.Revision != idle.Revision || got.NoClientSince != idle.NoClientSince {
				t.Fatalf("poll changed idle %+v", got)
			}
			c = open()
			reconnected := wait(1)
			if reconnected.Revision <= idle.Revision || reconnected.NoClientSince != "" {
				t.Fatal("reconnect not recorded")
			}
			c.CloseNow()
			wait(0)
		})
	}
}

func TestPluginForwardIdleWatchAtomicDeadline(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	cmd, args := taskSleeper(t)
	task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "tool", Command: cmd, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "tool"}, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WatchForward(t.Context(), "two", f.ID, .05); err == nil {
		t.Fatal("other owner watch")
	}
	if err := s.WatchForward(t.Context(), "one", f.ID, .05); err != nil {
		t.Fatal(err)
	}
	// Drive upgrade/close under the same lock used by real downstream hijacks.
	reg := &s.pluginForwards
	reg.Lock()
	st := reg.entries[f.ID]
	oldRevision := st.activity.Revision
	s.forwardSocketOpenedLocked(f.ID, st)
	reg.Unlock()
	s.expireForward(f.ID, st, oldRevision) // A queued stale timer must lose to reconnect.
	time.Sleep(80 * time.Millisecond)
	tasks, _ := s.plugins.m.Tasks("one")
	if tasks[0].State != "running" {
		t.Fatal("active browser killed")
	}
	reg.Lock()
	s.forwardSocketClosedLocked(f.ID, st)
	reg.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tasks, _ = s.plugins.m.Tasks("one")
		if tasks[0].State == "exited" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle deadline not applied")
		}
		time.Sleep(time.Millisecond)
	}
	if tasks[0].Reason != "idle" {
		t.Fatalf("reason %+v", tasks[0])
	}
	if fs, _ := s.ListForwards(context.Background(), "one"); len(fs) != 0 {
		t.Fatal("idle forward leaked")
	}
}

func TestPluginForwardNeverConnectedDeadline(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	cmd, args := taskSleeper(t)
	task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "unused", Command: cmd, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "unused"}, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, duration := range []float64{-1, 0, .001, 604801} {
		if err := s.WatchForward(t.Context(), "one", f.ID, duration); err == nil {
			t.Fatalf("invalid duration %v", duration)
		}
	}
	if err := s.WatchForward(t.Context(), "one", f.ID, .02); err != nil {
		t.Fatal(err)
	}
	eventually(t, "unused task stops on deadline", func() bool {
		tasks, _ := s.plugins.m.Tasks("one")
		return tasks[0].State == "exited" && tasks[0].Reason == "idle"
	})
	if own, _ := s.ListForwards(t.Context(), "one"); len(own) != 0 {
		t.Fatal("never-connected deadline left forward")
	}
}

func TestPluginRemovedWatchedForwardStopsTaskBeforeStaleExpiry(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	cmd, args := taskSleeper(t)
	task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "removed", Command: cmd, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "removed"}, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	// A separate tracker makes the commit-to-forget window deterministic: the
	// server's real event follower cannot erase this pending callback's record.
	expiry := &Server{board: s.board, log: s.log}
	expiry.plugins.m = s.plugins.m
	st := &pluginForwardState{plugin: "one", task: task.ID, since: time.Now().Add(-2 * time.Second), idle: time.Second, activity: plugins.ForwardActivity{Revision: 1}}
	expiry.pluginForwards.entries = map[string]*pluginForwardState{f.ID: st}
	revision := st.activity.Revision
	if err := s.board.DeleteProxyForward(t.Context(), board.User, f.ID); err != nil {
		t.Fatal(err)
	}
	expiry.expireForward(f.ID, st, revision)
	tasks, _ := s.plugins.m.Tasks("one")
	if tasks[0].State != "exited" || tasks[0].Reason != "forward_removed" {
		t.Fatalf("removed watched forward retained task: %+v", tasks[0])
	}
}
