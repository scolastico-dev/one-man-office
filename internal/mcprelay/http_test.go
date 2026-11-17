package mcprelay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

func httpServer(name, url string) api.MCPServer {
	return api.MCPServer{
		Name:      name,
		Enabled:   true,
		Transport: api.MCPTransportHTTP,
		URL:       url,
		Timeouts:  api.MCPTimeouts{Startup: api.Duration(10 * time.Second)},
	}
}

// recorder notes the Authorization and X-Api-Key headers of every request.
type recorder struct {
	mu   sync.Mutex
	seen []string
	next http.Handler
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.seen = append(r.seen, req.Header.Get("Authorization")+"|"+req.Header.Get("X-Api-Key")+"|"+req.Header.Get("Cookie"))
	r.mu.Unlock()
	r.next.ServeHTTP(w, req)
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.seen)
}

func TestHTTPAndSSEServers(t *testing.T) {
	streamable := httptest.NewServer(fakemcp.Handler(fakemcp.Config{}))
	t.Cleanup(streamable.Close)
	sse := httptest.NewServer(fakemcp.SSEHandler(fakemcp.Config{}))
	t.Cleanup(sse.Close)
	legacy := httpServer("legacy", sse.URL)
	legacy.Transport = api.MCPTransportSSE
	f := newFixture(t, nil, httpServer("remote", streamable.URL), legacy)
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	tools, err := f.pool.Tools(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	names := toolNames(tools)
	if !slices.Contains(names, "remote__echo") || !slices.Contains(names, "legacy__echo") {
		t.Fatalf("tools %v", names)
	}
	for _, tool := range []string{"remote__echo", "legacy__echo"} {
		if res := f.call(id, tool, map[string]string{"text": "x"}); resultText(res) != "echo: x" {
			t.Fatalf("%s answered %q", tool, resultText(res))
		}
	}
	// One shared connection for every session.
	f.call(agent("other", t.TempDir(), api.RoleDeveloper), "remote__echo", nil)
	if st := f.state("remote"); st.Instances != 1 {
		t.Fatalf("instances %d", st.Instances)
	}
}

func TestAnUnreachableServerIsAToolError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	s := httpServer("gone", url)
	s.Timeouts.Startup = api.Duration(2 * time.Second)
	f := newFixture(t, nil, s)
	res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "gone__echo", nil)
	if !res.IsError || !strings.HasPrefix(resultText(res), "server gone is not reachable: ") {
		t.Fatalf("answered %q", resultText(res))
	}
}

func TestStaticHeadersAndHeaderCommand(t *testing.T) {
	rec := &recorder{next: fakemcp.Handler(fakemcp.Config{})}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	s := httpServer("hdr", srv.URL)
	s.Auth = api.MCPAuthHeaders
	s.Headers = map[string]string{"Authorization": "Bearer ${HDR_TOKEN}"}
	s.HeadersCommand = []string{exe()}
	f := newFixture(t, nil, s)
	f.env = append(f.env, "HDR_TOKEN=static-token-value", envHeadersCommand+`={"X-Api-Key":"from-the-command"}`)
	if res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "hdr__echo", map[string]string{"text": "x"}); res.IsError {
		t.Fatalf("answered %q", resultText(res))
	}
	for _, seen := range rec.all() {
		if seen != "Bearer static-token-value|from-the-command|" {
			t.Fatalf("the server saw %q", seen)
		}
	}
}

func TestAuthNoneSendsNoHeaders(t *testing.T) {
	rec := &recorder{next: fakemcp.Handler(fakemcp.Config{})}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	s := httpServer("plain", srv.URL)
	s.Auth = api.MCPAuthNone
	s.Headers = map[string]string{"X-Api-Key": "not-sent-value"}
	f := newFixture(t, nil, s)
	f.call(agent("s", t.TempDir(), api.RoleDeveloper), "plain__echo", nil)
	for _, seen := range rec.all() {
		if seen != "||" {
			t.Fatalf("the server saw %q", seen)
		}
	}
}

// TestNoTokenPassthrough: whatever credential a client presented to omo,
// the upstream sees only what omo holds for that server.
func TestNoTokenPassthrough(t *testing.T) {
	const inbound = "omo_pat_inbound-client-credential-123"
	rec := &recorder{next: fakemcp.Handler(fakemcp.Config{})}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	withHeaders := httpServer("withheaders", srv.URL)
	withHeaders.Headers = map[string]string{"X-Api-Key": "omo-own-key-for-upstream"}
	withHeaders.Global = true
	bare := httpServer("bare", srv.URL)
	bare.Global = true
	f := newFixture(t, nil, withHeaders, bare)

	// The request that reached omo, as the endpoint's middlewares leave it
	// in the context.
	inReq := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	inReq.Header.Set("Authorization", "Bearer "+inbound)
	inReq.Header.Set("Cookie", "omo="+inbound)
	var ctx context.Context
	verify := func(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
		return &auth.TokenInfo{UserID: "pat:1", Expiration: time.Now().Add(time.Hour)}, nil
	}
	auth.RequireBearerToken(verify, nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx = r.Context()
	})).ServeHTTP(httptest.NewRecorder(), inReq)
	if auth.TokenInfoFromContext(ctx) == nil {
		t.Fatal("no inbound token in the context")
	}
	id := userClass()
	id.Via = "pat"
	if _, err := f.pool.Tools(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"withheaders__echo", "bare__echo"} {
		raw, _ := json.Marshal(map[string]string{"text": "hi"})
		if res, err := f.pool.Call(ctx, id, tool, raw, nil); err != nil || res.IsError {
			t.Fatalf("%s: %v %v", tool, err, res)
		}
	}
	seen := rec.all()
	if len(seen) == 0 {
		t.Fatal("the upstream saw no request")
	}
	for _, s := range seen {
		if strings.Contains(s, inbound) || strings.Contains(s, "Bearer") {
			t.Fatalf("the upstream saw %q", s)
		}
		if s != "||" && s != "|omo-own-key-for-upstream|" {
			t.Fatalf("the upstream saw %q", s)
		}
	}
}

func TestTheHeaderCommandRunsAgainAfterA401(t *testing.T) {
	var mu sync.Mutex
	accepted := "first-key-value"
	next := fakemcp.Handler(fakemcp.Config{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ok := r.Header.Get("X-Api-Key") == accepted
		mu.Unlock()
		if !ok {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "headers.json")
	write := func(key string) {
		if err := os.WriteFile(file, []byte(`{"X-Api-Key":"`+key+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("first-key-value")
	s := httpServer("rotating", srv.URL)
	s.HeadersCommand = []string{exe()}
	f := newFixture(t, nil, s)
	f.env = append(f.env, envHeadersCommand+"=@"+file)
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	if res := f.call(id, "rotating__echo", map[string]string{"text": "a"}); resultText(res) != "echo: a" {
		t.Fatalf("answered %q", resultText(res))
	}
	mu.Lock()
	accepted = "second-key-value"
	mu.Unlock()
	write("second-key-value")
	if res := f.call(id, "rotating__echo", map[string]string{"text": "b"}); resultText(res) != "echo: b" {
		t.Fatalf("after the key rotated: %q", resultText(res))
	}
}
