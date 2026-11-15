package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/service"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

// testHome isolates OMO_HOME and opens its layout.
func testHome(t *testing.T) globalhome.Layout {
	t.Helper()
	t.Setenv("OMO_HOME", t.TempDir())
	l, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// testConfig returns the default start options on a free loopback port.
func testConfig(mutate ...func(*serverconfig.Config)) serverconfig.Config {
	c := serverconfig.Defaults()
	c.Port = 0
	for _, m := range mutate {
		m(&c)
	}
	return c
}

// newTestServer opens a server (no listeners) on a fresh home.
func newTestServer(t *testing.T, mutate ...func(*serverconfig.Config)) *Server {
	t.Helper()
	return openServer(t, testHome(t), testConfig(mutate...))
}

func openServer(t *testing.T, l globalhome.Layout, c serverconfig.Config) *Server {
	t.Helper()
	s, err := newServer(Options{Layout: l, Config: c, Version: "test"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// browser serves s.BrowserHandler on a real loopback listener, so the Host
// check sees the connection's port.
type browser struct {
	t    *testing.T
	srv  *httptest.Server
	port string
}

func newBrowser(t *testing.T, s *Server) *browser {
	t.Helper()
	srv := httptest.NewServer(s.BrowserHandler())
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return &browser{t: t, srv: srv, port: port}
}

// do sends a request with Host omo.localhost:<port> unless hdr sets "Host".
func (b *browser) do(method, path string, body any, hdr map[string]string) *http.Response {
	b.t.Helper()
	req := newRequest(b.t, method, b.srv.URL+path, body)
	req.Host = hostOmoLocalhost + ":" + b.port
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := b.srv.Client().Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	b.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func newRequest(t *testing.T, method, url string, body any) *http.Request {
	t.Helper()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	default:
		// Every state-changing API request needs the JSON Content-Type,
		// body or not.
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func bearer(s *Server) map[string]string {
	return map[string]string{"Authorization": "Bearer " + s.token}
}

// socketDo sends a request through s.SocketHandler; session "" sends no
// session header.
func socketDo(t *testing.T, s *Server, method, path string, body any, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(t, method, "http://omo"+path, body)
	if session != "" {
		req.Header.Set(SessionHeader, session)
	}
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	return rec
}

func wantStatus(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("status %d, want %d", got, want)
	}
}

// errorCode decodes the error envelope's code.
func errorCode(t *testing.T, body io.Reader) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return env.Error.Code
}

func decodeBody[T any](t *testing.T, body io.Reader) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// newRepo creates a git repository without reading the developer's git
// configuration.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

// liveSession creates a live agent session of role; developer and pm
// sessions get a ticket of their own.
func liveSession(t *testing.T, s *Server, role api.Role) api.AgentSession {
	t.Helper()
	ctx := context.Background()
	ns := board.NewSession{Role: role, Name: "agent"}
	if role != api.RoleCEO {
		p, err := s.settings.AddProject(newRepo(t), "")
		if err != nil {
			t.Fatal(err)
		}
		s.waitSettingsEvents(ctx)
		typ := api.TypeTask
		if role == api.RolePM {
			typ = api.TypeStory
		}
		tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: typ, Title: "work", Projects: []string{p.ID}})
		if err != nil {
			t.Fatal(err)
		}
		ns.Ticket = int64(tk.Number)
	}
	sess, err := s.board.CreateSession(ctx, ns)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)
	resp := newBrowser(t, s).do("GET", "/", nil, nil)
	for k, v := range map[string]string{
		"Cache-Control":           "no-store",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": contentSecurityPolicy,
	} {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestReservedRoutesAnswerNotImplemented(t *testing.T) {
	s := newTestServer(t)
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/tickets/1/export"},
		{"GET", "/api/terminals"},
		{"POST", "/api/projects/rescan"},
		{"GET", "/api/usage"},
		{"GET", "/api/plugins/x"},
	} {
		rec := socketDo(t, s, c.method, c.path, nil, "")
		wantStatus(t, rec.Code, http.StatusNotImplemented)
		if code := errorCode(t, rec.Body); code != api.CodeNotImplemented {
			t.Fatalf("%s %s: code %q", c.method, c.path, code)
		}
	}
	rec := socketDo(t, s, "GET", "/api/nothing", nil, "")
	wantStatus(t, rec.Code, http.StatusNotFound)
	if code := errorCode(t, rec.Body); code != api.CodeNotFound {
		t.Fatalf("unknown route code %q", code)
	}
}

func TestServerInfoAndPause(t *testing.T) {
	s := newTestServer(t)
	rec := socketDo(t, s, "PUT", "/api/server/paused", map[string]bool{"paused": true}, "")
	wantStatus(t, rec.Code, http.StatusOK)
	info := decodeBody[api.ServerInfo](t, rec.Body)
	if !info.Paused || info.Version != "test" || info.StartedAt.IsZero() {
		t.Fatalf("info %+v", info)
	}
	// The change is committed before the answer.
	if paused, err := s.board.Paused(context.Background()); err != nil || !paused {
		t.Fatalf("paused %v, %v", paused, err)
	}
	rec = socketDo(t, s, "PUT", "/api/server/paused", map[string]string{}, "")
	wantStatus(t, rec.Code, http.StatusBadRequest)
}

func TestPausedRequestRequiresPausedKey(t *testing.T) {
	s := newTestServer(t)
	for _, body := range []string{`{}`, `{"paused":null}`} {
		req := httptest.NewRequest("PUT", "http://omo/api/server/paused", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.SocketHandler().ServeHTTP(rec, req)
		wantStatus(t, rec.Code, http.StatusBadRequest)
		if code := errorCode(t, rec.Body); code != api.CodeBadRequest {
			t.Fatalf("body %s: code %q", body, code)
		}
	}
	// An explicit false is accepted and unpauses.
	yes, no := true, false
	wantStatus(t, socketDo(t, s, "PUT", "/api/server/paused", api.PausedRequest{Paused: &yes}, "").Code, http.StatusOK)
	rec := socketDo(t, s, "PUT", "/api/server/paused", api.PausedRequest{Paused: &no}, "")
	wantStatus(t, rec.Code, http.StatusOK)
	if decodeBody[api.ServerInfo](t, rec.Body).Paused {
		t.Fatal("still paused after paused:false")
	}
}

func TestDecodeRejectsBadBodies(t *testing.T) {
	s := newTestServer(t)
	send := func(body, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "http://omo/api/server/paused", strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		s.SocketHandler().ServeHTTP(rec, req)
		return rec
	}
	for _, c := range []struct {
		body, contentType, code string
	}{
		{`{"paused":true,"x":1}`, "application/json", api.CodeBadRequest},
		{`{"paused":true}{}`, "application/json", api.CodeBadRequest},
		{``, "application/json", api.CodeBadRequest},
		{`{"paused":true}`, "text/plain", api.CodeBadRequest},
		{`{"paused":"` + strings.Repeat("x", maxBody) + `"}`, "application/json", api.CodeTooLarge},
	} {
		rec := send(c.body, c.contentType)
		if code := errorCode(t, rec.Body); code != c.code || rec.Code != api.StatusFor(c.code) {
			t.Errorf("body %.30q: %d %q, want %q", c.body, rec.Code, code, c.code)
		}
	}
	if rec := send(`{"paused":true}`, "application/json; charset=utf-8"); rec.Code != http.StatusOK {
		t.Fatalf("valid body: %d", rec.Code)
	}
}

func TestTokenSurvivesRestart(t *testing.T) {
	l := testHome(t)
	s := openServer(t, l, testConfig())
	raw, err := os.ReadFile(l.Token)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))
	if token != s.token {
		t.Fatalf("server token differs from the token file")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := openServer(t, l, testConfig())
	resp := newBrowser(t, s2).do("GET", "/api/server", nil, map[string]string{"Authorization": "Bearer " + token})
	wantStatus(t, resp.StatusCode, http.StatusOK)
}

func TestRotatedTokenInvalidatesOld(t *testing.T) {
	l := testHome(t)
	s := openServer(t, l, testConfig())
	old := s.token
	s.Close()
	fresh, err := serverconfig.RotateToken(l.Token)
	if err != nil {
		t.Fatal(err)
	}
	s2 := openServer(t, l, testConfig())
	b := newBrowser(t, s2)
	resp := b.do("GET", "/api/server", nil, map[string]string{"Authorization": "Bearer " + old})
	wantStatus(t, resp.StatusCode, http.StatusUnauthorized)
	resp = b.do("GET", "/api/server", nil, map[string]string{"Authorization": "Bearer " + fresh})
	wantStatus(t, resp.StatusCode, http.StatusOK)
}

// syncBuffer is a bytes.Buffer safe for concurrent use.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startRun runs Run in the background and waits until it is ready.
func startRun(t *testing.T, o Options, out io.Writer) (url string, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	readyc := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, o, out, func(u string) error { readyc <- u; return nil })
	}()
	select {
	case url = <-readyc:
	case err := <-done:
		cancel()
		t.Fatalf("Run ended before ready: %v", err)
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatal("Run not ready")
	}
	var once sync.Once
	var stopErr error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case stopErr = <-done:
			case <-time.After(shutdownTimeout + 2*time.Second):
				stopErr = errors.New("Run did not stop in time")
			}
		})
		return stopErr
	}
	t.Cleanup(func() { stop() })
	return url, stop
}

// socketClient talks HTTP over the server's socket.
func socketClient(l globalhome.Layout) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			ep, err := transport.ClientEndpoint(l.Run)
			if err != nil {
				return nil, err
			}
			return transport.Dial(ctx, ep)
		},
	}}
}

func TestRunPrintsOmoLocalhostAddress(t *testing.T) {
	l := testHome(t)
	out := &syncBuffer{}
	url, stop := startRun(t, Options{Layout: l, Config: testConfig(), Version: "test"}, out)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	first := regexp.MustCompile(`^omo: http://omo\.localhost:(\d+)/#([0-9a-f]{64})$`).FindStringSubmatch(lines[0])
	if first == nil {
		t.Fatalf("first line %q", lines[0])
	}
	raw, err := os.ReadFile(l.Token)
	if err != nil {
		t.Fatal(err)
	}
	if first[2] != strings.TrimSpace(string(raw)) {
		t.Fatal("printed token differs from the token file")
	}
	if url != strings.TrimPrefix(lines[0], "omo: ") {
		t.Fatalf("ready URL %q differs from the printed line %q", url, lines[0])
	}
	if len(lines) < 2 || lines[1] != "also: http://127.0.0.1:"+first[1]+"/#"+first[2] {
		t.Fatalf("second line %q", lines[1])
	}

	// The printed address answers through the TCP listener.
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+first[1]+"/api/server", nil)
	req.Host = "omo.localhost:" + first[1]
	req.Header.Set("Authorization", "Bearer "+first[2])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	info := decodeBody[api.ServerInfo](t, resp.Body)
	resp.Body.Close()
	if info.Address != "http://omo.localhost:"+first[1]+"/" {
		t.Fatalf("address %q", info.Address)
	}

	// The socket answers without a token; the caller is the user.
	resp, err = socketClient(l).Get("http://omo/api/me")
	if err != nil {
		t.Fatal(err)
	}
	me := decodeBody[api.Me](t, resp.Body)
	resp.Body.Close()
	if me.Actor.Role != api.RoleUser {
		t.Fatalf("socket caller %q", me.Actor.Role)
	}

	start := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > shutdownTimeout {
		t.Fatalf("shutdown took %v", d)
	}
	assertReleased(t, l, first[1])
}

// assertReleased checks that a stopped server left no listener or socket
// behind.
func assertReleased(t *testing.T, l globalhome.Layout, port string) {
	t.Helper()
	if port != "" {
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			t.Fatalf("TCP port still taken: %v", err)
		}
		ln.Close()
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Lstat(filepath.Join(l.Run, "omo.sock")); !os.IsNotExist(err) {
			t.Fatalf("socket link left behind: %v", err)
		}
	}
}

func TestRunReleasesEverythingOnFailedStart(t *testing.T) {
	l := testHome(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	err = Run(context.Background(), Options{Layout: l, Config: testConfig(func(c *serverconfig.Config) { c.Port = port })}, io.Discard, nil)
	if err == nil {
		t.Fatal("Run on a taken port succeeded")
	}
	assertReleased(t, l, "")

	// A failing ready callback releases both listeners.
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	port = free.Addr().(*net.TCPAddr).Port
	free.Close()
	o := Options{Layout: l, Config: testConfig(func(c *serverconfig.Config) { c.Port = port })}
	want := errors.New("not ready")
	if err := Run(context.Background(), o, io.Discard, func(string) error { return want }); !errors.Is(err, want) {
		t.Fatalf("Run = %v, want the ready error", err)
	}
	assertReleased(t, l, strconv.Itoa(port))

	// The home is usable again afterwards.
	_, stop := startRun(t, o, io.Discard)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

func TestRunRemovesStaleSocketFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes leave no folder")
	}
	l := testHome(t)
	stale, err := os.MkdirTemp("", "omo-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(stale) })
	if err := os.Symlink(filepath.Join(stale, "omo.sock"), filepath.Join(l.Run, "omo.sock")); err != nil {
		t.Fatal(err)
	}
	_, stop := startRun(t, Options{Layout: l, Config: testConfig()}, io.Discard)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale socket folder kept: %v", err)
	}
	stop()
}

func TestSecondStartDetectsRunningServer(t *testing.T) {
	l := testHome(t)
	o := Options{Layout: l, Config: testConfig(), Version: "test"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- service.Run(ctx, io.Discard, "", func(ctx context.Context, out io.Writer, ready func(string) error) error {
			return Run(ctx, o, out, ready)
		})
	}()
	defer func() {
		cancel()
		<-done
	}()
	var url string
	deadline := time.Now().Add(15 * time.Second)
	for {
		u, running, err := service.Running(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if running {
			url = u
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never reported running")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.HasPrefix(url, "http://omo.localhost:") {
		t.Fatalf("running URL %q", url)
	}
	err := service.Run(context.Background(), io.Discard, "", func(context.Context, io.Writer, func(string) error) error {
		t.Error("second service.Run served")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second service.Run = %v", err)
	}
}

func TestBackgroundRefusedAfterClose(t *testing.T) {
	for range 50 {
		s := openServer(t, testHome(t), testConfig())
		var started, afterClose atomic.Int32
		var closed atomic.Bool
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.background(func(ctx context.Context) {
					started.Add(1)
					if closed.Load() {
						afterClose.Add(1)
					}
					<-ctx.Done()
				})
			}()
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		closed.Store(true)
		wg.Wait()
		if s.background(func(context.Context) { afterClose.Add(1) }) {
			t.Fatal("background accepted work after Close")
		}
		if n := afterClose.Load(); n != 0 {
			t.Fatalf("%d workers ran after Close returned", n)
		}
	}
}

func TestJSONContentTypeRequiredOnBodylessRequests(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	// send sends a body-less request with exactly the given Content-Type
	// (none when empty) through both listeners' handlers.
	send := func(method, path, contentType string) map[string]int {
		hdr := bearer(s)
		hdr["Content-Type"] = contentType
		got := map[string]int{"browser": b.do(method, path, nil, hdr).StatusCode}
		req := httptest.NewRequest(method, "http://omo"+path, nil)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		s.SocketHandler().ServeHTTP(rec, req)
		got["socket"] = rec.Code
		return got
	}
	// Reserved routes read no body and answer 501 once they are reached.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/projects/rescan"},
		{"DELETE", "/api/terminals/x"},
		{"POST", "/api/no/such/route"},
	} {
		for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
			for name, status := range send(c.method, c.path, contentType) {
				if status != http.StatusBadRequest {
					t.Errorf("%s %s %s (%q): status %d, want 400", name, c.method, c.path, contentType, status)
				}
			}
		}
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/projects/rescan"},
		{"DELETE", "/api/terminals/x"},
	} {
		for _, contentType := range []string{"application/json", "application/json; charset=utf-8"} {
			for name, status := range send(c.method, c.path, contentType) {
				if status != http.StatusNotImplemented {
					t.Errorf("%s %s %s (%q): status %d, want 501", name, c.method, c.path, contentType, status)
				}
			}
		}
	}
	// The error is the one decode gives.
	hdr := bearer(s)
	hdr["Content-Type"] = ""
	resp := b.do("POST", "/api/projects/rescan", nil, hdr)
	if code := errorCode(t, resp.Body); code != api.CodeBadRequest {
		t.Fatalf("code %q", code)
	}
	// Safe methods and the page need no Content-Type.
	wantStatus(t, b.do("GET", "/api/server", nil, bearer(s)).StatusCode, http.StatusOK)
	wantStatus(t, b.do("GET", "/", nil, nil).StatusCode, http.StatusOK)
}
