package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/transport"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/testpath"
)

func installTaskPlugin(t *testing.T, s *Server, name string) {
	t.Helper()
	dir := filepath.Join(s.opts.Layout.Plugins, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"hooks":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.PutPluginIf("", api.PluginSettings{Name: name, Source: testpath.Abs("/local/plugin"), Enabled: true, Config: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	s.reloadPlugins(true)
}

func TestPluginTaskRoutes(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	installTaskPlugin(t, s, "two")
	rec := socketDo(t, s, "GET", "/api/plugins/one/tasks", nil, "")
	if rec.Code != 200 || rec.Body.String() != "{\"tasks\":[]}\n" {
		t.Fatalf("empty tasks: %d %s", rec.Code, rec.Body)
	}
	// A real child gives stop and the subsequent refreshed GET observable state.
	cmd, args := taskSleeper(t)
	task, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "editor", Command: cmd, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/plugins/two/tasks/editor/stop", "/api/plugins/missing/tasks/editor/stop", "/api/plugins/one/tasks/absent/stop"} {
		rec = socketDo(t, s, "POST", path, map[string]any{}, "")
		if rec.Code != 404 {
			t.Fatalf("wrong owner %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	rec = socketDo(t, s, "POST", "/api/plugins/one/tasks/editor/stop", map[string]any{"unexpected": true}, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON: %d %s", rec.Code, rec.Body)
	}
	for range 2 {
		rec = socketDo(t, s, "POST", "/api/plugins/one/tasks/editor/stop", map[string]any{}, "")
		if rec.Code != 204 {
			t.Fatalf("stop: %d %s", rec.Code, rec.Body)
		}
	}
	rec = socketDo(t, s, "GET", "/api/plugins/one/tasks", nil, "")
	var list api.PluginTasks
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].ID != task.ID || list.Tasks[0].State != "exited" || list.Tasks[0].Reason != "stopped" {
		t.Fatalf("refresh: %+v", list)
	}
	sess := liveSession(t, s, api.RoleDeveloper)
	for _, req := range []struct{ method, path string }{{"GET", "/api/plugins/one/tasks"}, {"POST", "/api/plugins/one/tasks/editor/stop"}} {
		rec = socketDo(t, s, req.method, req.path, map[string]any{}, sess.ID)
		if rec.Code != 403 {
			t.Fatalf("agent %s: %d", req.path, rec.Code)
		}
	}
}

func TestPluginTaskTicketCompletionAndCascade(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	cmd, args := taskSleeper(t)
	for _, n := range []int{9999, -1} {
		if _, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "invalid", Command: cmd, Args: args, Ticket: n}); err == nil {
			t.Fatal("invalid ticket accepted")
		}
	}
	story, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeStory, Title: "Story"})
	if err != nil {
		t.Fatal(err)
	}
	parent := story.Number
	child, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "Child", Story: &parent})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{parent, child.Number} {
		if _, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: fmt.Sprint(n), Command: cmd, Args: args, Ticket: n}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.board.Cancel(t.Context(), board.User, int64(parent), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "cascade task cleanup", func() bool {
		ts, _ := s.plugins.m.Tasks("one")
		for _, x := range ts {
			if x.State == "running" {
				return false
			}
		}
		return true
	})
	if _, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "closed", Command: cmd, Args: args, Ticket: child.Number}); err == nil {
		t.Fatal("cancelled ticket accepted")
	}
}

// This binary's helper is shared by server process lifecycle tests.
func taskSleeper(t *testing.T) (string, []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe, []string{"plugin-task-sleeper"}
}

func TestPluginTaskShutdown(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	cmd, args := taskSleeper(t)
	if _, err := s.plugins.m.StartTask(context.Background(), "one", plugins.TaskStart{Name: "server", Command: cmd, Args: args}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	tasks, _ := s.plugins.m.Tasks("one")
	if len(tasks) != 1 || tasks[0].Reason != "shutdown" || tasks[0].EndedAt == "" {
		t.Fatalf("shutdown: %+v", tasks)
	}
}

func taskSocketProbe() int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return transport.Dial(ctx, os.Getenv("OMO_SOCKET"))
	}}}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://omo/api/plugins/one/tasks", nil)
	resp, err := client.Do(req)
	if err != nil {
		return 2
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 3
	}
	f, err := os.OpenFile(os.Args[2], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 4
	}
	defer f.Close()
	fmt.Fprintln(f, "ready")
	return 0
}

func TestPluginStartupAfterRealSocketReadiness(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	file := filepath.Join(t.TempDir(), "startup")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.opts.Layout.Plugins, "one")
	raw, _ := json.Marshal(map[string]any{"hooks": []any{map[string]any{"event": "omo_started", "lua": "start.lua"}}})
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`local _,err=omo.exec(%q,"plugin-task-probe",%q); assert(err=="",err)`, exe, file)
	if err := os.WriteFile(filepath.Join(dir, "start.lua"), []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	opts := s.opts
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts, io.Discard, nil) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	eventually(t, "startup socket/API probe", func() bool { raw, _ := os.ReadFile(file); return string(raw) == "ready\n" })
}

func TestPluginTaskNormalCompletion(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	tk, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "Complete", Review: api.ReviewHuman})
	if err != nil {
		t.Fatal(err)
	}
	cmd, args := taskSleeper(t)
	if _, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "normal", Command: cmd, Args: args, Ticket: tk.Number}); err != nil {
		t.Fatal(err)
	}
	// Seed only the review stage; completion goes through the real board rule.
	if _, err := s.db.Exec(`UPDATE tickets SET col='review',review_stage='human' WHERE id=?`, tk.Number); err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Approve(t.Context(), board.User, int64(tk.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "completion cleanup", func() bool {
		ts, _ := s.plugins.m.Tasks("one")
		return len(ts) == 1 && ts[0].State == "exited" && ts[0].Reason == "ticket_done"
	})
}

func taskOutputHelper() int {
	fmt.Println("first line")
	for {
		if _, err := os.Stat(os.Args[2]); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Println("second line")
	time.Sleep(time.Minute)
	return 0
}

func TestPluginTaskOutputRefreshEvents(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	gate := filepath.Join(t.TempDir(), "next-output")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "output", Command: exe, Args: []string{"plugin-task-output", gate}})
	if err != nil {
		t.Fatal(err)
	}
	eventCount := func() int {
		evs, err := db.EventsSince(s.db, 0, 10000, true)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, ev := range evs {
			if ev.Kind == api.KindPluginTasksChanged {
				var d api.PluginTasksChangedData
				json.Unmarshal(toAPIEvent(ev).Data, &d)
				if d.Name == "one" {
					n++
				}
			}
		}
		return n
	}
	eventually(t, "first output event", func() bool { return eventCount() >= 1 })
	first := eventCount()
	if err := os.WriteFile(gate, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "second output event", func() bool { return eventCount() > first })
	rec := socketDo(t, s, "GET", "/api/plugins/one/tasks", nil, "")
	var list api.PluginTasks
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Tasks) != 1 || len(list.Tasks[0].Output) != 2 || list.Tasks[0].Output[1] != "second line" {
		t.Fatalf("refreshed output: %s", rec.Body)
	}
	second := eventCount()
	if err := s.plugins.m.StopTask("one", "output"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "stop event", func() bool { return eventCount() > second })
}

func TestPluginTaskCleanupBypassesFullHookQueue(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	dir := filepath.Join(s.opts.Layout.Plugins, "one")
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"hooks":[{"event":"ticket_created","lua":"slow.lua"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "slow.lua"), []byte(`omo.store_set("busy",true); omo.sleep(10)`), 0600); err != nil {
		t.Fatal(err)
	}
	s.reloadPlugins(true)
	tk, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "Cancel"})
	if err != nil {
		t.Fatal(err)
	}
	cmd, args := taskSleeper(t)
	if _, err := s.plugins.m.StartTask(t.Context(), "one", plugins.TaskStart{Name: "bound", Command: cmd, Args: args, Ticket: tk.Number}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "blocking hook", func() bool { raw, _, _ := db.PluginStoreGet(s.db, "one", "busy"); return raw == "true" })
	for range plugins.QueueSize + 10 {
		s.plugins.m.Dispatch(plugins.Event{Name: plugins.EventTicketCreated})
	}
	if _, err := s.board.Cancel(t.Context(), board.User, int64(tk.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			tasks, _ := s.plugins.m.Tasks("one")
			if len(tasks) == 1 && tasks[0].Reason == "ticket_done" && tasks[0].State == "exited" {
				return
			}
		}
	}
}

func TestPluginTaskOutputEventCadence(t *testing.T) {
	s := newTestServer(t)
	installTaskPlugin(t, s, "one")
	base := time.Now().Add(time.Minute)
	after := lastEventID(t, s)
	// A noisy writer invalidates continuously for a minute. The scheduler
	// permits only one output refresh per ten-second window for this plugin.
	for second := 0; second < 60; second++ {
		for range 100 {
			s.pluginTaskOutputChanged("one")
		}
		s.flushPluginTaskEvents(base.Add(time.Duration(second) * time.Second))
	}
	events, err := db.EventsSince(s.db, after, 1000, true)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Kind == api.KindPluginTasksChanged {
			count++
		}
	}
	if count != 6 {
		t.Fatalf("continuous output recorded %d events in a minute, want 6", count)
	}
	// Lifecycle changes do not wait behind the output throttle.
	s.pluginTasksChanged("one")
	s.flushPluginTaskEvents(base.Add(59 * time.Second))
	events, err = db.EventsSince(s.db, after, 1000, true)
	if err != nil {
		t.Fatal(err)
	}
	count = 0
	for _, event := range events {
		if event.Kind == api.KindPluginTasksChanged {
			count++
		}
	}
	if count != 7 {
		t.Fatalf("lifecycle invalidation was delayed: %d", count)
	}
}
