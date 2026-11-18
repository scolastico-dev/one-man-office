package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

func removalTask(t *testing.T, s *Server, ticket int) (api.PluginTask, plugins.Forward) {
	t.Helper()
	cmd, args := taskSleeper(t)
	task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "tool-" + strconv.Itoa(ticket), Command: cmd, Args: args, Ticket: ticket})
	if err != nil {
		t.Fatal(err)
	}
	req := plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "tool"}, TaskID: task.ID}
	if ticket > 0 {
		req.Ticket = &ticket
	}
	f, err := s.AddForward(t.Context(), "one", req)
	if err != nil {
		t.Fatal(err)
	}
	return task, f
}

func TestPluginWatchedRemovalStopsOutsideLock(t *testing.T) {
	for _, via := range []string{"api", "lua", "board"} {
		t.Run(via, func(t *testing.T) {
			s := newTestServer(t)
			installTaskPlugin(t, s, "one")
			if via == "lua" {
				dir := filepath.Join(s.opts.Layout.Plugins, "one")
				if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"hooks":[{"event":"manual","name":"remove","description":"Remove","scope":"global","lua":"remove.lua"}]}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "remove.lua"), []byte(`local f=omo.forward_list()[1]; assert(omo.forward_remove(f.id));return "removed"`), 0600); err != nil {
					t.Fatal(err)
				}
				s.reloadPlugins(true)
			}
			task, f := removalTask(t, s, 0)
			if err := s.WatchForward(t.Context(), "one", f.ID, 3600); err != nil {
				t.Fatal(err)
			}
			s.pluginForwards.Lock()
			st := s.pluginForwards.entries[f.ID]
			revision := st.activity.Revision
			s.pluginForwards.Unlock()
			switch via {
			case "api":
				rec := socketDo(t, s, "DELETE", "/api/proxy/forwards/"+f.ID, nil, "")
				if rec.Code != 204 {
					t.Fatalf("DELETE %d %s", rec.Code, rec.Body)
				}
			case "lua":
				rec := socketDo(t, s, "POST", "/api/plugins/one/actions/remove", api.PluginRun{}, "")
				if rec.Code != 200 {
					t.Fatalf("helper %d %s", rec.Code, rec.Body)
				}
			case "board":
				if err := s.board.DeleteProxyForward(t.Context(), board.User, f.ID); err != nil {
					t.Fatal(err)
				}
				s.stopTasksForEvent(boardEvent(t, 0, api.KindProxyForwardRemoved, f.ProxyForward))
			}

			ts, _ := s.plugins.m.Tasks("one")
			if ts[0].State != "exited" || ts[0].Reason != "forward_removed" {
				t.Fatalf("removal reason %+v", ts)
			}
			// An already queued deadline and repeated event must not touch a reused name.
			cmd, args := taskSleeper(t)
			next, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: task.Name, Command: cmd, Args: args})
			if err != nil {
				t.Fatal(err)
			}
			s.expireForward(f.ID, st, revision)
			s.stopTasksForEvent(boardEvent(t, 0, api.KindProxyForwardRemoved, f.ProxyForward))
			ts, _ = s.plugins.m.Tasks("one")
			for _, record := range ts {
				if record.ID == next.ID && record.State != "running" {
					t.Fatal("stale removal stopped reused task name")
				}
			}
		})
	}
}

func TestPluginUserDeleteWatchedForwardWithUpgradedStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer upstream.Close()
	s := newTestServer(t)
	b := newBrowser(t, s)
	installTaskPlugin(t, s, "one")
	task, f := removalTask(t, s, 0)
	// The independent upstream keeps its upgraded connection open after task
	// shutdown, exercising teardown without relying on process/socket coupling.
	if err := s.RemoveForward(t.Context(), "one", f.ID); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: port, Name: "live", StripPrefix: true}, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WatchForward(t.Context(), "one", f.ID, 3600); err != nil {
		t.Fatal(err)
	}
	h := proxyLogin(t, s, b, f.ProxyForward)
	c, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(b.srv.URL, "http")+f.BasePath, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	eventually(t, "active downstream", func() bool {
		fs, _ := s.ListForwards(t.Context(), "one")
		return len(fs) == 1 && fs[0].Activity.ActiveWebSockets == 1
	})
	res := asUser(b, s, "DELETE", "/api/proxy/forwards/"+f.ID, nil)
	wantStatus(t, res.StatusCode, 204)
	res.Body.Close()
	ts, _ := s.plugins.m.Tasks("one")
	if ts[0].State != "exited" || ts[0].Reason != "forward_removed" {
		t.Fatalf("active removal %+v", ts)
	}
	if err := c.Write(t.Context(), websocket.MessageText, []byte("still upgraded")); err != nil {
		t.Fatal(err)
	}
	_, data, err := c.Read(t.Context())
	if err != nil || string(data) != "still upgraded" {
		t.Fatalf("existing stream %s %v", data, err)
	}
	c.CloseNow()
	eventually(t, "closed upgrade cannot resurrect state", func() bool {
		s.pluginForwards.Lock()
		defer s.pluginForwards.Unlock()
		return len(s.pluginForwards.entries) == 0
	})
}

func TestPluginWatchedTicketCascadeRemoval(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	story, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeStory, Title: "story"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "child", Story: &story.Number})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{story.Number, child.Number} {
		_, f := removalTask(t, s, n)
		if err := s.WatchForward(t.Context(), "one", f.ID, 3600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.board.Cancel(t.Context(), board.User, int64(story.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "cascade reaps watched tasks", func() bool {
		ts, _ := s.plugins.m.Tasks("one")
		return len(ts) == 2 && ts[0].State == "exited" && ts[1].State == "exited"
	})
	ts, _ := s.plugins.m.Tasks("one")
	for _, task := range ts {
		if task.Reason != "ticket_done" {
			t.Fatalf("cascade reason %+v", task)
		}
	}
	fs, _ := s.board.PluginForwards(context.Background(), board.System, "one")
	if len(fs) != 0 {
		t.Fatal("cascade left forwards")
	}
}

func TestPluginWatchedDirectBoardRemoval(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	_, f := removalTask(t, s, 0)
	if err := s.WatchForward(t.Context(), "one", f.ID, 3600); err != nil {
		t.Fatal(err)
	}
	if err := s.board.DeleteProxyForward(t.Context(), board.User, f.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "reliable removal event stops watched task", func() bool {
		ts, _ := s.plugins.m.Tasks("one")
		return ts[0].State == "exited" && ts[0].Reason == "forward_removed"
	})
}

func TestPluginWatchedNormalTicketDone(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	tk, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "done", Review: api.ReviewHuman})
	if err != nil {
		t.Fatal(err)
	}
	_, f := removalTask(t, s, tk.Number)
	if err := s.WatchForward(t.Context(), "one", f.ID, 3600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tickets SET col='review',review_stage='human' WHERE id=?`, tk.Number); err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Approve(t.Context(), board.User, int64(tk.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Done keeps ticket cleanup reason", func() bool {
		ts, _ := s.plugins.m.Tasks("one")
		return ts[0].State == "exited" && ts[0].Reason == "ticket_done"
	})
}

func TestPluginUnwatchedRemovalKeepsTask(t *testing.T) {
	for _, via := range []string{"api", "helper", "board"} {
		t.Run(via, func(t *testing.T) {
			s := newTestServer(t)
			installTaskPlugin(t, s, "one")
			_, f := removalTask(t, s, 0)
			switch via {
			case "api":
				rec := socketDo(t, s, "DELETE", "/api/proxy/forwards/"+f.ID, nil, "")
				if rec.Code != 204 {
					t.Fatalf("DELETE %d", rec.Code)
				}
			case "helper":
				if err := s.RemoveForward(t.Context(), "one", f.ID); err != nil {
					t.Fatal(err)
				}
			case "board":
				if err := s.board.DeleteProxyForward(t.Context(), board.User, f.ID); err != nil {
					t.Fatal(err)
				}
			}
			s.stopTasksForEvent(boardEvent(t, 0, api.KindProxyForwardRemoved, f.ProxyForward))
			ts, _ := s.plugins.m.Tasks("one")
			if ts[0].State != "running" {
				t.Fatal("unwatched removal stopped task")
			}
		})
	}
}
