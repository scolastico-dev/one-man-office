package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	dir := t.TempDir()
	actual, _, cleanup, err := transport.Endpoint(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := transport.Listen(actual)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(func() { srv.Close(); cleanup() })
	return actual
}

func TestSessionHeaderAndJSONContentType(t *testing.T) {
	var gotSession, gotType, gotBody string
	ep := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession, gotType = r.Header.Get(SessionHeader), r.Header.Get("Content-Type")
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		b, _ := json.Marshal(m)
		gotBody = string(b)
		w.Write([]byte(`{"actor":{"role":"developer"},"ticket":4}`))
	}))
	c := New(ep, "abc")
	me, err := c.Me(context.Background())
	if err != nil || me.Actor.Role != api.RoleDeveloper || *me.Ticket != 4 || gotSession != "abc" {
		t.Fatalf("%+v %v %q", me, err, gotSession)
	}
	if _, err := c.AddDependency(context.Background(), 1, 2); err != nil || gotType != "application/json" || gotBody != `{"depends_on":2}` {
		t.Fatalf("%v %q %s", err, gotType, gotBody)
	}
	if string(c.LastBody()) == "" {
		t.Fatal("no last body")
	}
	if New(ep, "").session != "" {
		t.Fatal("session set")
	}
}

func TestErrorEnvelopeBecomesAPIError(t *testing.T) {
	ep := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		w.Write([]byte(`{"error":{"code":"validation","message":"title is required"}}`))
	}))
	err := New(ep, "").Do(context.Background(), "POST", "/api/tickets", struct{}{}, nil)
	e, ok := err.(*api.Error)
	if !ok || e.Code != "validation" || e.Message != "title is required" || e.Status != 422 {
		t.Fatalf("%#v", err)
	}
}

func TestNotRunning(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	t.Setenv("OMO_SOCKET", "")
	if _, err := FromEnvironment(); err != ErrNotRunning {
		t.Fatalf("link missing: %v", err)
	}
	t.Setenv("OMO_SOCKET", filepath.Join(t.TempDir(), "nothing.sock"))
	if _, err := FromEnvironment(); err != ErrNotRunning {
		t.Fatalf("socket dead: %v", err)
	}
	c := New(filepath.Join(t.TempDir(), "x.sock"), "")
	if err := c.Do(context.Background(), "GET", "/api/me", nil, nil); err != ErrNotRunning {
		t.Fatalf("request: %v", err)
	}
}

func TestFromEnvironmentUsesSocketVariable(t *testing.T) {
	ep := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	t.Setenv("OMO_SOCKET", ep)
	t.Setenv("OMO_AGENT_ID", "sid")
	c, err := FromEnvironment()
	if err != nil || c.session != "sid" {
		t.Fatalf("%v", err)
	}
	var _ net.Conn
}

func TestFromEnvironmentRefusesEmptyAgentID(t *testing.T) {
	requests := 0
	ep := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write([]byte(`{}`)) }))
	t.Setenv("OMO_SOCKET", ep)
	for _, v := range []string{"", "  ", "\t\n"} {
		t.Setenv("OMO_AGENT_ID", v)
		if _, err := FromEnvironment(); !errors.Is(err, ErrEmptyAgentID) || strings.Contains(err.Error(), "\n") {
			t.Fatalf("%q: error %v", v, err)
		}
	}
	os.Unsetenv("OMO_AGENT_ID")
	c, err := FromEnvironment()
	if err != nil || c.session != "" {
		t.Fatalf("unset: %v", err)
	}
	if requests != 0 {
		t.Fatalf("%d requests sent", requests)
	}
}
