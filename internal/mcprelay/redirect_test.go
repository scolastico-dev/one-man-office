package mcprelay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

// otherOrigin is a server on another origin that records what it is sent.
func otherOrigin(t *testing.T) (*recorder, *httptest.Server) {
	t.Helper()
	rec := &recorder{next: fakemcp.Handler(fakemcp.Config{})}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return rec, srv
}

func headerServer(name, u string) api.MCPServer {
	s := httpServer(name, u)
	s.Auth = api.MCPAuthHeaders
	s.Headers = map[string]string{"Authorization": "Bearer header-secret-value", "X-Api-Key": "api-key-secret-value"}
	return s
}

// A server that redirects to another origin never gets omo's headers
// there: omo does not follow the redirect.
func TestARedirectToAnotherOriginGetsNoCredentials(t *testing.T) {
	rec, other := otherOrigin(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	f := newFixture(t, nil, headerServer("moved", srv.URL+"/mcp"))
	if res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "moved__echo", nil); !res.IsError {
		t.Fatalf("answered %q", resultText(res))
	}
	if seen := rec.all(); len(seen) != 0 {
		t.Fatalf("the other origin was sent %v", seen)
	}
}

// An SSE server that names a message endpoint on another origin is
// refused; that origin gets nothing.
func TestAnSSEEndpointOnAnotherOriginIsRefused(t *testing.T) {
	rec, other := otherOrigin(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: endpoint\ndata: %s/messages\n\n", other.URL)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	s := headerServer("sse", srv.URL+"/sse")
	s.Transport = api.MCPTransportSSE
	f := newFixture(t, nil, s)
	if res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "sse__echo", nil); !res.IsError {
		t.Fatalf("answered %q", resultText(res))
	}
	if seen := rec.all(); len(seen) != 0 {
		t.Fatalf("the other origin was sent %v", seen)
	}
}
