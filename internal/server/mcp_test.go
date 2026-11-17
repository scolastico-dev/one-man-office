package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/mcpserver"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

// headerTransport sets the Host and extra headers of every request.
type headerTransport struct {
	host   string
	header map[string]string
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if h.host != "" {
		r.Host = h.host
	}
	for k, v := range h.header {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// mcpSocket serves s.SocketHandler as the socket does.
func mcpSocket(t *testing.T, s *Server) string {
	t.Helper()
	srv := httptest.NewServer(s.SocketHandler())
	t.Cleanup(srv.Close)
	return srv.URL + "/mcp"
}

// mcpTCP serves s.BrowserHandler on loopback; it returns the /mcp URL and
// the Host header the listener accepts.
func mcpTCP(t *testing.T, s *Server) (string, string) {
	t.Helper()
	srv := httptest.NewServer(s.BrowserHandler())
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return srv.URL + "/mcp", hostOmoLocalhost + ":" + port
}

func mcpConnect(t *testing.T, endpoint string, rt headerTransport) (*mcp.ClientSession, error) {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: &http.Client{Transport: rt}, MaxRetries: -1,
	}, nil)
	if err == nil {
		t.Cleanup(func() { cs.Close() })
	}
	return cs, err
}

// viaSocket connects as session over the socket ("" is no session).
func viaSocket(t *testing.T, s *Server, session string) *mcp.ClientSession {
	t.Helper()
	hdr := map[string]string{}
	if session != "" {
		hdr[SessionHeader] = session
	}
	cs, err := mcpConnect(t, mcpSocket(t, s), headerTransport{header: hdr})
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// viaBearer connects over TCP with the bearer token ("" sends none).
func viaBearer(t *testing.T, s *Server, token string) *mcp.ClientSession {
	t.Helper()
	cs, err := bearerConnect(t, s, token)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func bearerConnect(t *testing.T, s *Server, token string) (*mcp.ClientSession, error) {
	t.Helper()
	url, host := mcpTCP(t, s)
	hdr := map[string]string{}
	if token != "" {
		hdr["Authorization"] = "Bearer " + token
	}
	return mcpConnect(t, url, headerTransport{host: host, header: hdr})
}

func callTool(cs *mcp.ClientSession, name string, args any) (*mcp.CallToolResult, error) {
	return cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
}

func toolText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func listToolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tool := range res.Tools {
		out = append(out, tool.Name)
	}
	return out
}

// identityRelay offers one tool to everyone and records the identity of
// every call.
type identityRelay struct {
	mu    sync.Mutex
	calls map[string][]mcpserver.Identity // by the Instance the tool was asked for
}

func (r *identityRelay) Tools(context.Context, mcpserver.Identity) ([]mcpserver.RelayedTool, error) {
	return []mcpserver.RelayedTool{{Name: "fs__whoami", Server: "fs", Tool: &mcp.Tool{Name: "whoami", InputSchema: map[string]any{"type": "object"}}}}, nil
}

func (r *identityRelay) Call(_ context.Context, id mcpserver.Identity, _ string, args json.RawMessage, _ func(float64, float64, string)) (*mcp.CallToolResult, error) {
	var in struct{ Expect string }
	json.Unmarshal(args, &in)
	r.mu.Lock()
	r.calls[in.Expect] = append(r.calls[in.Expect], id)
	r.mu.Unlock()
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: id.Instance + " " + id.Workdir}}}, nil
}

func (r *identityRelay) Generation() uint64 { return 1 }

// inProgress puts the ticket of a developer session in progress, where it
// can be handed in.
func inProgress(t *testing.T, s *Server, ticket int64) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE tickets SET col = ? WHERE id = ?`, api.ColumnInProgress, ticket); err != nil {
		t.Fatal(err)
	}
}

func sessionTicket(t *testing.T, sess api.AgentSession) int {
	t.Helper()
	if sess.Ticket == nil {
		t.Fatal("session without a ticket")
	}
	return *sess.Ticket
}

// Five developer sessions call tools concurrently, half over the socket
// and half with a bearer over TCP: every answer, comment, relayed call and
// hand-in belongs to the caller's own session and ticket.
func TestParallelSessionsStayOnTheirTickets(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) { c.Auth = "basic"; c.BasicAuth = "u:p" })
	relay := &identityRelay{calls: map[string][]mcpserver.Identity{}}
	s.mcp.relay = relay
	const sessions, rounds = 5, 20
	var devs []api.AgentSession
	for range sessions {
		d := liveSession(t, s, api.RoleDeveloper)
		inProgress(t, s, int64(sessionTicket(t, d)))
		devs = append(devs, d)
	}
	clients := make([]*mcp.ClientSession, sessions)
	for i, d := range devs {
		if i%2 == 0 {
			clients[i] = viaSocket(t, s, d.ID)
		} else {
			clients[i] = viaBearer(t, s, d.ID)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, sessions*rounds*4)
	for i, d := range devs {
		wg.Go(func() {
			<-start
			cs, own := clients[i], sessionTicket(t, d)
			fail := func(format string, args ...any) {
				errs <- fmt.Errorf("session %d: "+format, append([]any{i}, args...)...)
			}
			for r := range rounds {
				res, err := callTool(cs, "whoami", nil)
				if err != nil || res.IsError || !strings.Contains(toolText(res), fmt.Sprintf("ticket: #%d\n", own)) {
					fail("whoami %v %v", err, res)
				}
				res, err = callTool(cs, "ticket_show", map[string]any{})
				if err != nil || res.IsError || !strings.Contains(toolText(res), fmt.Sprintf("#%d", own)) {
					fail("ticket_show %v %v", err, res)
				}
				res, err = callTool(cs, "comment", map[string]any{"ticket": own, "body": fmt.Sprintf("from %d round %d", i, r)})
				if err != nil || res.IsError {
					fail("comment %v %v", err, res)
				}
				res, err = callTool(cs, "fs__whoami", map[string]any{"Expect": d.ID})
				if err != nil || res.IsError {
					fail("relayed %v %v", err, res)
				}
			}
			res, err := callTool(cs, "done", map[string]any{"comment": fmt.Sprintf("done by %d", i)})
			if err != nil || res.IsError {
				fail("done %v %v", err, res)
			}
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for _, d := range devs {
		own := sessionTicket(t, d)
		rec := socketDo(t, s, "GET", fmt.Sprintf("/api/tickets/%d/comments", own), nil, "")
		wantStatus(t, rec.Code, http.StatusOK)
		mine := 0
		for _, c := range decodeBody[[]api.Comment](t, rec.Body) {
			if c.Author.Role == api.RoleDeveloper {
				if c.Author.Name != d.Name || c.Ticket != own {
					t.Errorf("ticket %d holds a comment by %+v", own, c.Author)
				}
				mine++
			}
		}
		if mine != rounds+1 {
			t.Errorf("ticket %d has %d developer comments, want %d", own, mine, rounds+1)
		}
		tk, err := s.board.Get(context.Background(), board.User, int64(own))
		if err != nil || tk.Column != api.ColumnReview {
			t.Errorf("ticket %d after the hand-in: %v %v", own, tk.Column, err)
		}
		for _, id := range relay.calls[d.ID] {
			if id.Class != mcpserver.ClassAgent || id.Session != d.ID || id.Instance != d.ID || id.Workdir != d.Workdir {
				t.Errorf("relayed call for %s carried %+v", d.ID, id)
			}
		}
		if len(relay.calls[d.ID]) != rounds {
			t.Errorf("%d relayed calls for %s", len(relay.calls[d.ID]), d.ID)
		}
	}
}

// What session A's credential may do on session B's ticket over MCP is
// exactly what `omo ai` with A's session may do; an ended session's
// credential stops working on both paths, and B goes on.
func TestSessionTokenRefusedForOtherTicket(t *testing.T) {
	s := newTestServer(t)
	a, b := liveSession(t, s, api.RoleDeveloper), liveSession(t, s, api.RoleDeveloper)
	other := sessionTicket(t, b)
	cli := apiclient.NewWithTransport(apiclient.HandlerTransport(s.SocketHandler()), a.ID)
	png := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n0000"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]map[string]any{
		"comment":       {"ticket": other, "body": "not mine"},
		"ticket_update": {"ticket": other, "title": "taken"},
		"ticket_move":   {"ticket": other, "column": "done"},
		"image_add":     {"ticket": other, "path": png},
	}
	for name, path := range map[string]*mcp.ClientSession{"socket": viaSocket(t, s, a.ID), "bearer": viaBearer(t, s, a.ID)} {
		for tool, args := range cases {
			res, err := callTool(path, tool, args)
			if err != nil {
				t.Fatalf("%s %s: %v", name, tool, err)
			}
			want := runAsCLI(t, cli, tool, args)
			if !strings.HasSuffix(want, "(forbidden)") {
				t.Errorf("%s: omo ai lets A act on B's ticket: %q", tool, want)
			}
			if res.IsError != (want != "") || (want != "" && toolText(res) != want) {
				t.Errorf("%s %s: MCP %v %q, omo ai %q", name, tool, res.IsError, toolText(res), want)
			}
		}
	}
	if tk, _ := s.board.Get(context.Background(), board.User, int64(other)); tk.Title == "taken" || tk.Column == api.ColumnDone {
		t.Fatalf("A changed B's ticket: %+v", tk)
	}

	sockA, bearA := viaSocket(t, s, a.ID), viaBearer(t, s, a.ID)
	sockB := viaSocket(t, s, b.ID)
	if err := s.board.EndSession(context.Background(), a.ID, "test"); err != nil {
		t.Fatal(err)
	}
	for name, cs := range map[string]*mcp.ClientSession{"socket": sockA, "bearer": bearA} {
		if _, err := callTool(cs, "whoami", nil); err == nil {
			t.Errorf("%s: the ended session still calls tools", name)
		}
	}
	rec := socketMCP(t, s, a.ID)
	wantStatus(t, rec.Code, http.StatusUnauthorized)
	if code := errorCode(t, rec.Body); code != api.CodeUnknownSession {
		t.Fatalf("code %q", code)
	}
	if _, err := bearerConnect(t, s, a.ID); err == nil {
		t.Error("a new bearer connection of the ended session was accepted")
	}
	if res, err := callTool(sockB, "whoami", nil); err != nil || res.IsError {
		t.Fatalf("B after A ended: %v %v", err, res)
	}
}

// runAsCLI runs the tool's command as `omo ai` does; it returns the
// refusal, or "" when the command succeeded.
func runAsCLI(t *testing.T, c *apiclient.Client, tool string, args map[string]any) string {
	t.Helper()
	for _, cmd := range aicmd.Commands() {
		if cmd.Tool != tool {
			continue
		}
		in := reflect.New(cmd.Input)
		data, _ := json.Marshal(args)
		if err := json.Unmarshal(data, in.Interface()); err != nil {
			t.Fatal(err)
		}
		if _, err := cmd.Run(context.Background(), c, in.Interface()); err != nil {
			return aicmd.Explain(err).Error()
		}
		return ""
	}
	t.Fatalf("no command %s", tool)
	return ""
}

// socketMCP posts a tools/list request to /mcp on the socket.
func socketMCP(t *testing.T, s *Server, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "http://omo/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		req.Header.Set(SessionHeader, session)
	}
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	return rec
}

// A connection omo cannot tie to an agent session gets no omo tools: a
// call to one tells it to use `omo ai` in the terminal.
func TestMCPWithoutSessionGetsNoOmoTools(t *testing.T) {
	check := func(t *testing.T, cs *mcp.ClientSession) {
		t.Helper()
		if got := listToolNames(t, cs); len(got) != 0 {
			t.Errorf("tools %v", got)
		}
		if !strings.Contains(cs.InitializeResult().Instructions, "not tied to an omo agent session") {
			t.Errorf("instructions %q", cs.InitializeResult().Instructions)
		}
		res, err := callTool(cs, "ticket_list", map[string]any{})
		if err != nil || !res.IsError || !strings.Contains(toolText(res), "use `omo ai ticket list` in the terminal") {
			t.Errorf("ticket_list: %v %v", err, res)
		}
	}
	t.Run("socket", func(t *testing.T) { check(t, viaSocket(t, newTestServer(t), "")) })
	t.Run("auth none", func(t *testing.T) {
		check(t, viaBearer(t, newTestServer(t, func(c *serverconfig.Config) { c.Auth = "none" }), ""))
	})
	t.Run("personal token", func(t *testing.T) {
		s := newTestServer(t)
		_, secret, err := s.mcpAuth.tokens.CreatePAT(context.Background(), "laptop", nil)
		if err != nil {
			t.Fatal(err)
		}
		check(t, viaBearer(t, s, secret))
	})
}

func TestMCPBearerRefusals(t *testing.T) {
	s := newTestServer(t)
	url, host := mcpTCP(t, s)
	post := func(hdr map[string]string) *http.Response {
		req, _ := http.NewRequest("POST", url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		for k, v := range hdr {
			if k == "Host" {
				req.Host = v
				continue
			}
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	for name, hdr := range map[string]map[string]string{
		"no bearer":      {},
		"login token":    {"Authorization": "Bearer " + s.token},
		"unknown token":  {"Authorization": "Bearer omo_pat_nothing"},
		"unknown agent":  {"Authorization": "Bearer 0123456789abcdef0123456789abcdef"},
		"session header": {SessionHeader: liveSession(t, s, api.RoleCEO).ID},
	} {
		resp := post(hdr)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d", name, resp.StatusCode)
		}
		if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, `resource_metadata="http://`+host+`/.well-known/oauth-protected-resource/mcp"`) {
			t.Errorf("%s: WWW-Authenticate %q", name, got)
		}
	}
	if resp := post(map[string]string{"Host": "evil.example"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign host: %d", resp.StatusCode)
	}
}

// Failing calls never put a credential or an argument into the log.
func TestNoCredentialInMCPLog(t *testing.T) {
	var logBuf bytes.Buffer
	s, err := newServer(Options{Layout: testHome(t), Config: testConfig(), Version: "test"}, &syncWriter{w: &logBuf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	dev := liveSession(t, s, api.RoleDeveloper)
	const secret = "omo_pat_s3cr3tvalue"
	if _, err := bearerConnect(t, s, secret); err == nil {
		t.Fatal("an unknown token was accepted")
	}
	cs := viaBearer(t, s, dev.ID)
	for _, args := range []map[string]any{{"ticket": 999, "body": "argument-value-xyz"}, {"ticket": "bad", "body": "argument-value-xyz"}} {
		if res, err := callTool(cs, "comment", args); err != nil || !res.IsError {
			t.Fatalf("comment %v: %v %v", args, err, res)
		}
	}
	s.board.EndSession(context.Background(), dev.ID, "test")
	callTool(cs, "whoami", nil)
	for _, v := range []string{secret, dev.ID, "argument-value-xyz", s.token} {
		if strings.Contains(logBuf.String(), v) {
			t.Errorf("the log holds %q:\n%s", v, logBuf.String())
		}
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// The user's agent list marks the sessions that used omo's MCP endpoint.
func TestAgentsListMarksMCPSessions(t *testing.T) {
	s := newTestServer(t)
	used, unused := liveSession(t, s, api.RoleDeveloper), liveSession(t, s, api.RoleDeveloper)
	viaSocket(t, s, used.ID)
	rec := socketDo(t, s, "GET", "/api/agents?live=true", nil, "")
	wantStatus(t, rec.Code, http.StatusOK)
	marks := map[string]string{}
	for _, a := range decodeBody[[]api.AgentSession](t, rec.Body) {
		marks[a.ID] = a.MCP
	}
	if marks[used.ID] != api.MCPAgentConnected || marks[unused.ID] != "" {
		t.Fatalf("marks %v", marks)
	}
}
