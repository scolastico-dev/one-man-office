package server

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

func TestPluginForwardTaskAttachmentAndIsolation(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	installTaskPlugin(t, s, "two")
	cmd, args := taskSleeper(t)
	start := func() api.PluginTask {
		t.Helper()
		task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "tool", Command: cmd, Args: args})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	task := start()
	f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "tool"}, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if f.TaskID != task.ID || f.Activity.ActiveWebSockets != 0 || f.Activity.NoClientSince == "" {
		t.Fatalf("created %+v", f)
	}
	if own, err := s.ListForwards(t.Context(), "two"); err != nil || len(own) != 0 {
		t.Fatalf("other plugin list %+v %v", own, err)
	}
	if err := s.RemoveForward(t.Context(), "two", f.ID); err == nil {
		t.Fatal("other plugin removed forward")
	}
	if err := s.AttachForward(t.Context(), "two", f.ID, task.ID); err == nil {
		t.Fatal("other plugin attached forward")
	}
	if err := s.plugins.m.StopTaskID("one", task.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	if own, err := s.ListForwards(t.Context(), "one"); err != nil || len(own) != 0 {
		t.Fatalf("exit leaked forward: %+v %v", own, err)
	}
	if _, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "dead"}, TaskID: task.ID}); err == nil {
		t.Fatal("attached exited task")
	}
	// Reused names never redirect cleanup from an older stable identity.
	next := start()
	unbound, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "base first"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachForward(t.Context(), "one", unbound.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	s.pluginTaskExited("one", task)
	if own, _ := s.ListForwards(t.Context(), "one"); len(own) != 1 {
		t.Fatal("old identity deleted replacement")
	}
	// Stop and attachment race under the same resource lock, never orphaning.
	for range 12 {
		var wg sync.WaitGroup
		wg.Go(func() { _ = s.AttachForward(context.Background(), "one", unbound.ID, next.ID) })
		wg.Go(func() { _ = s.plugins.m.StopTaskID("one", next.ID, "stopped") })
		wg.Wait()
	}
	if own, _ := s.ListForwards(t.Context(), "one"); len(own) != 0 {
		t.Fatal("attach/exit orphan")
	}
}

func TestPluginForwardTicketBindingAndLoginSecrecy(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	cmd, args := taskSleeper(t)
	tk, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "bound"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "bound", Command: cmd, Args: args, Ticket: tk.Number})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "global"}, TaskID: task.ID}); err == nil {
		t.Fatal("global forward attached ticket task")
	}
	req := plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Ticket: &tk.Number, Port: 3456, Name: "bound"}, TaskID: task.ID}
	f, err := s.AddForward(t.Context(), "one", req)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/?folder=" + s.token, "/?folder=" + url.QueryEscape(s.token)} {
		req.StartPath = path
		if _, err := s.AddForward(t.Context(), "one", req); err == nil {
			t.Fatal("forward leaked current login token")
		}
	}
	if _, err := s.board.Cancel(t.Context(), board.User, int64(tk.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "ticket cleanup stops attached task", func() bool { tasks, _ := s.plugins.m.Tasks("one"); return tasks[0].State == "exited" })
	if _, err := s.board.LiveProxyForward(t.Context(), board.System, f.ID); err == nil {
		t.Fatal("closed ticket forward survived")
	}
	req.StartPath = "/"
	req.TaskID = ""
	if _, err := s.AddForward(t.Context(), "one", req); err == nil {
		t.Fatal("closed ticket accepted forward")
	}
}

func TestPluginForwardEveryTaskAndPluginExit(t *testing.T) {
	for _, reason := range []string{"exited", "stopped", "ticket_done", "plugin_disabled", "plugin_removed", "plugin_replaced", "shutdown"} {
		t.Run(reason, func(t *testing.T) {
			s := newTestServer(t)
			installTaskPlugin(t, s, "one")
			cmd, args := taskSleeper(t)
			req := plugins.TaskStart{Name: "tool", Command: cmd, Args: args}
			var ticket *int
			if reason == "ticket_done" {
				tk, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "tool"})
				if err != nil {
					t.Fatal(err)
				}
				n := tk.Number
				ticket = &n
				req.Ticket = n
			}
			task, err := s.plugins.m.StartTask(t.Context(), "one", req)
			if err != nil {
				t.Fatal(err)
			}
			f, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Ticket: ticket, Port: 3456, Name: "tool"}, TaskID: task.ID})
			if err != nil {
				t.Fatal(err)
			}
			// Unattached forwards also belong to their plugin's enabled/code lifecycle.
			if reason == "plugin_disabled" || reason == "plugin_removed" || reason == "plugin_replaced" || reason == "shutdown" {
				if _, err := s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "unbound"}}); err != nil {
					t.Fatal(err)
				}
			}
			switch reason {
			case "plugin_disabled":
				p, _ := s.settings.Plugin("one")
				p.Enabled = false
				if err := s.settings.PutPluginIf(p.Revision, p); err != nil {
					t.Fatal(err)
				}
				s.reloadPlugins(true)
			case "plugin_removed":
				s.plugins.m.Load(nil)
			case "plugin_replaced":
				if err := os.WriteFile(filepath.Join(s.opts.Layout.Plugins, "one/plugin.json"), []byte(`{"version":"1.0.1","hooks":[]}`), 0600); err != nil {
					t.Fatal(err)
				}
				s.reloadPlugins(true)
			case "shutdown":
				s.plugins.m.Close()
			case "ticket_done":
				if _, err := s.board.Cancel(t.Context(), board.User, int64(*ticket), api.CancelRequest{}); err != nil {
					t.Fatal(err)
				}
				s.plugins.m.StopTicketTasks(*ticket)
			default:
				if err := s.plugins.m.StopTaskID("one", task.ID, reason); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.board.LiveProxyForward(t.Context(), board.System, f.ID); err == nil {
				t.Fatal("task forward survived exit")
			}
			all, err := s.board.PluginForwards(t.Context(), board.System, "one")
			if err != nil || len(all) != 0 {
				t.Fatalf("resources survived %s: %+v %v", reason, all, err)
			}
		})
	}
}

func TestPluginForwardCreateExitRace(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	cmd, args := taskSleeper(t)
	for range 20 {
		task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "quick", Command: cmd, Args: append([]string{}, args...)})
		if err != nil {
			t.Fatal(err)
		}
		// This loop exercises the exit-versus-add race; Stop guarantees callback
		// completion and identity retention before the following assertion.
		done := make(chan struct{})
		go func() { _ = s.plugins.m.StopTaskID("one", task.ID, "exited"); close(done) }()
		_, _ = s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "race"}, TaskID: task.ID})
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("exit blocked")
		}
		fs, _ := s.board.PluginForwards(t.Context(), board.System, "one")
		if len(fs) != 0 {
			t.Fatal("exit/create orphan")
		}
	}
}

func TestPluginForwardLifecycleAndStaleCleanup(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	user, err := s.board.CreateProxyForward(t.Context(), board.User, api.CreateProxyForward{Port: 3456, Name: "user"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "plugin"}})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.settings.Plugin("one")
	p.Enabled = false
	if err := s.settings.PutPluginIf(p.Revision, p); err != nil {
		t.Fatal(err)
	}
	s.reloadPlugins(true)
	all, err := s.board.ProxyForwards(t.Context(), board.User, nil)
	if err != nil || len(all) != 1 || all[0].ID != user.ID {
		t.Fatalf("disable: %+v %v", all, err)
	}
	_, err = s.board.CreatePluginForward(t.Context(), board.System, "stale", "dead", api.CreateProxyForward{Port: 3456, Name: "stale"})
	if err != nil {
		t.Fatal(err)
	}
	options := s.opts
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := newServer(options, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	s = restarted
	all, err = s.board.ProxyForwards(t.Context(), board.User, nil)
	if err != nil || len(all) != 1 || all[0].ID != user.ID {
		t.Fatalf("restart cleanup: %+v %v", all, err)
	}
}

func TestPluginForwardNaturalTaskExit(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	cmd, _ := taskSleeper(t)
	gate := filepath.Join(t.TempDir(), "exit")
	task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "natural", Command: cmd, Args: []string{"plugin-task-gate", gate}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddForward(t.Context(), "one", plugins.ForwardAdd{CreateProxyForward: api.CreateProxyForward{Port: 3456, Name: "natural"}, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "natural exit cleans forward", func() bool { fs, _ := s.board.PluginForwards(t.Context(), board.System, "one"); return len(fs) == 0 })
	tasks, _ := s.plugins.m.Tasks("one")
	if len(tasks) != 1 || tasks[0].State != "exited" || tasks[0].Reason != "exited" {
		t.Fatalf("natural exit %+v", tasks)
	}
}
