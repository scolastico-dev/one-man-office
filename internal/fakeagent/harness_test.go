package fakeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/server"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

type stubEnv struct{}

func (stubEnv) Policy() board.Policy                     { return board.Policy{} }
func (stubEnv) Project(string) (board.ProjectInfo, bool) { return board.ProjectInfo{}, false }
func (stubEnv) Model(string) (board.ModelInfo, bool)     { return board.ModelInfo{}, false }
func (stubEnv) CurrentBranch(string) (string, error)     { return "main", nil }

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// world is a real in-process omo server without an engine on its socket,
// with one project. Its ready and wait routes are answered by standIn.
type world struct {
	t       *testing.T
	board   *board.Board
	user    *apiclient.Client
	project string
	socket  string

	mu       sync.Mutex
	requests []string
	// doneBodies are the bodies of POST /api/ai/done.
	doneBodies []string
	// waits feeds the stand-in for POST /api/ai/wait: each request takes one
	// reason, or blocks until its client goes away.
	waits chan string
}

// standIn answers ready and wait in front of the real server, so the ops
// can be tested in process against sessions the test creates itself. The
// real routes cannot serve these tests: without a runner they answer 503,
// and a running engine ends every live session it did not start ("agent
// process is gone"). The stand-in also produces what the real routes never
// do: a ready prompt that contains the caller's session id (to prove the
// fake never prints it) and scripted wait reasons, timeouts included, which
// the engine gives only after at least a minute. The real ready and wait
// routes are exercised with the fake agent as a process by the tests of
// internal/server and internal/e2e.
func (w *world) standIn(real http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/ai/ready", func(rw http.ResponseWriter, r *http.Request) {
		json.NewEncoder(rw).Encode(api.ReadyResponse{Prompt: "stand-in prompt for session " + r.Header.Get(apiclient.SessionHeader) + "\nrun your work"})
	})
	mux.HandleFunc("POST /api/ai/wait", func(rw http.ResponseWriter, r *http.Request) {
		select {
		case reason := <-w.waits:
			json.NewEncoder(rw).Encode(api.WaitResponse{Reason: reason})
		case <-r.Context().Done():
		}
	})
	mux.Handle("/", real)
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.requests = append(w.requests, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/ai/done" {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			w.doneBodies = append(w.doneBodies, string(body))
		}
		w.mu.Unlock()
		mux.ServeHTTP(rw, r)
	})
}

// calls returns the recorded requests to /api/ai/ and /api/tickets.
func (w *world) calls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, c := range w.requests {
		if strings.Contains(c, "/api/ai/") || strings.Contains(c, "/api/tickets") {
			if !strings.HasPrefix(c, "GET ") {
				out = append(out, c)
			}
		}
	}
	return out
}

func (w *world) seen(call string) bool {
	for _, c := range w.calls() {
		if c == call {
			return true
		}
	}
	return false
}

func (w *world) waitFor(call string) {
	w.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !w.seen(call) {
		if time.Now().After(deadline) {
			w.t.Fatalf("never saw %s; calls: %v", call, w.calls())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startWorld(t *testing.T) *world {
	t.Helper()
	t.Setenv("OMO_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OMO_SOCKET", "")
	t.Setenv("OMO_AGENT_ID", "")
	os.Unsetenv("OMO_AGENT_ID")
	for _, k := range []string{EnvScript, EnvScriptRolePrefix + "DEVELOPER", EnvScriptRolePrefix + "PM", EnvScriptRolePrefix + "REVIEWER"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	layout, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Options{Layout: layout, Config: serverconfig.Defaults(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	actual, _, cleanup, err := transport.Endpoint(layout.Run)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := transport.Listen(actual)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, user: apiclient.New(actual, ""), socket: actual, waits: make(chan string, 16)}
	httpSrv := &http.Server{Handler: w.standIn(srv.SocketHandler())}
	go httpSrv.Serve(ln)
	sqlDB, err := db.Open(layout.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		httpSrv.Close()
		cleanup()
		sqlDB.Close()
		srv.Close()
	})
	w.board = board.New(sqlDB, stubEnv{}, board.Options{})
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	var p api.Project
	if err := w.user.Do(context.Background(), http.MethodPost, "/api/projects", api.CreateProject{Path: repo, Name: "api"}, &p); err != nil {
		t.Fatal(err)
	}
	w.project = p.ID
	return w
}

// git runs git in dir with no user or system configuration.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// story creates a story and puts it In Progress as the engine would.
func (w *world) story(title string) int {
	w.t.Helper()
	ctx := context.Background()
	st, err := w.user.CreateTicket(ctx, api.CreateTicket{Type: api.TypeStory, Title: title, Projects: []string{w.project}, Review: api.ReviewHuman})
	if err != nil {
		w.t.Fatal(err)
	}
	w.begin(st.Number)
	return st.Number
}

// task creates a task of story and puts it In Progress.
func (w *world) task(title string, story int) int {
	w.t.Helper()
	tk, err := w.user.CreateTicket(context.Background(), api.CreateTicket{
		Type: api.TypeTask, Title: title, Projects: []string{w.project}, Story: &story, Review: api.ReviewHuman})
	if err != nil {
		w.t.Fatal(err)
	}
	w.begin(tk.Number)
	return tk.Number
}

func (w *world) begin(n int) {
	w.t.Helper()
	ctx := context.Background()
	if _, err := w.user.Move(ctx, n, api.MoveRequest{Column: api.ColumnReady}); err != nil {
		w.t.Fatal(err)
	}
	if err := w.board.BeginWork(ctx, int64(n)); err != nil {
		w.t.Fatal(err)
	}
}

// session creates a live session of role bound to ticket and makes the
// current test act as that agent: it sets OMO_AGENT_ID. It returns the id.
func (w *world) session(role api.Role, ticket int, name string) string {
	w.t.Helper()
	s, err := w.board.CreateSession(context.Background(), board.NewSession{Role: role, Ticket: int64(ticket), Name: name})
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Setenv("OMO_AGENT_ID", s.ID)
	return s.ID
}

func (w *world) ticket(n int) api.Ticket {
	w.t.Helper()
	tk, err := w.user.Ticket(context.Background(), n)
	if err != nil {
		w.t.Fatal(err)
	}
	return tk
}

// result is the outcome of one Run.
type result struct {
	code int
	out  string
}

// running is a Run in a goroutine.
type running struct {
	t      *testing.T
	out    *syncBuffer
	cancel context.CancelFunc
	done   chan struct{}
	code   int
}

// start runs the fake agent in the background; the test must call finish (or
// the cleanup cancels it).
func start(t *testing.T, o Options) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{t: t, out: &syncBuffer{}, cancel: cancel, done: make(chan struct{})}
	o.Stdout = r.out
	go func() {
		r.code = Run(ctx, o)
		close(r.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
		}
	})
	return r
}

// finish waits for the exit, or ends the agent after it ran into its wait.
func (r *running) finish(cancel bool) result {
	r.t.Helper()
	if cancel {
		r.cancel()
	}
	select {
	case <-r.done:
		return result{r.code, r.out.String()}
	case <-time.After(20 * time.Second):
		r.t.Fatalf("fake agent did not exit; output:\n%s", r.out.String())
		return result{}
	}
}

// runFake runs a script to its end.
func runFake(t *testing.T, o Options) result {
	t.Helper()
	return start(t, o).finish(false)
}
