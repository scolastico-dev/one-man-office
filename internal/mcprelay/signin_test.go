package mcprelay

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

// foreignHost is another MCP server that notes the Authorization headers
// it is sent.
func foreignHost(t *testing.T) (*recorder, string) {
	t.Helper()
	rec := &recorder{next: fakemcp.Handler(fakemcp.Config{})}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return rec, srv.URL + "/mcp"
}

// bearers are the Authorization values rec saw that carry a token.
func bearers(rec *recorder) []string {
	var out []string
	for _, seen := range rec.all() {
		if strings.HasPrefix(seen, "Bearer ") {
			out = append(out, seen)
		}
	}
	return out
}

func (f *fixture) storedToken(name string) string {
	f.t.Helper()
	a, ok, _ := f.store.Load(context.Background(), name)
	if !ok || a.Token == nil {
		return ""
	}
	return a.Token.AccessToken
}

// A sign-in belongs to the address, resource and client it was given for:
// after a change of any of them omo never sends its token to the server,
// and the stored sign-in is forgotten (and revoked).
func TestASignInIsBoundToWhatItWasGivenFor(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(s *api.MCPServer, other string)
	}{
		{"address", func(s *api.MCPServer, other string) { s.URL = other }},
		{"resource", func(s *api.MCPServer, _ string) { s.OAuth.Resource = "https://elsewhere.example/mcp" }},
		{"client", func(s *api.MCPServer, _ string) { s.OAuth.ClientID = "another-client" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			as := newFakeAS(t)
			rec, other := foreignHost(t)
			s := oauthServer("remote", as.url())
			f := newFixture(t, nil, s)
			if code, page := f.signIn("remote"); code != 200 {
				t.Fatalf("callback %d: %s", code, page)
			}
			token := f.storedToken("remote")
			c.change(&s, other)
			f.set(s)
			f.pool.Changed([]string{"remote"})

			id := agent("s", t.TempDir(), api.RoleDeveloper)
			if res := f.call(id, "remote__echo", nil); !strings.Contains(resultText(res), "needs a sign-in") {
				t.Fatalf("answered %q", resultText(res))
			}
			if st := f.state("remote"); st.State != api.MCPStateNeedsLogin {
				t.Fatalf("state %+v", st)
			}
			if got := bearers(rec); len(got) != 0 {
				t.Fatalf("the other server was sent %v", got)
			}
			if _, ok, _ := f.store.Load(context.Background(), "remote"); ok {
				t.Fatal("the sign-in of the old settings is still stored")
			}
			eventually(t, "the revocation", func() bool {
				as.mu.Lock()
				defer as.mu.Unlock()
				return slices.Contains(as.revoked, token)
			})
		})
	}
}

// A stored sign-in that does not match the settings is not used, also
// when the settings changed while omo did not run.
func TestAStoredSignInOfOtherSettingsIsNotUsed(t *testing.T) {
	as := newFakeAS(t)
	f := newFixture(t, nil, oauthServer("remote", as.url()))
	f.signIn("remote")
	rec, other := foreignHost(t)
	g := newFixture(t, nil, oauthServer("remote", other))
	g.store = f.store
	g.pool.store = f.store
	if st := g.state("remote"); st.State != api.MCPStateNeedsLogin {
		t.Fatalf("state %+v", st)
	}
	if res := g.call(agent("s", t.TempDir(), api.RoleDeveloper), "remote__echo", nil); !strings.Contains(resultText(res), "needs a sign-in") {
		t.Fatalf("answered %q", resultText(res))
	}
	if got := bearers(rec); len(got) != 0 {
		t.Fatalf("the other server was sent %v", got)
	}
}

// Deleting a server forgets and revokes its sign-in: a new server of the
// same name starts without one.
func TestDeletingAServerForgetsItsSignIn(t *testing.T) {
	as := newFakeAS(t)
	s := oauthServer("remote", as.url())
	f := newFixture(t, nil, s)
	f.signIn("remote")
	token := f.storedToken("remote")
	f.set()
	f.pool.Changed([]string{"remote"})
	if _, ok, _ := f.store.Load(context.Background(), "remote"); ok {
		t.Fatal("the sign-in of the deleted server is still stored")
	}
	eventually(t, "the revocation", func() bool {
		as.mu.Lock()
		defer as.mu.Unlock()
		return slices.Contains(as.revoked, token)
	})
	f.set(s)
	f.pool.Changed([]string{"remote"})
	if st := f.state("remote"); st.State != api.MCPStateNeedsLogin {
		t.Fatalf("the new server's state %+v", st)
	}
}

// Switching MCP off hides the servers but keeps their sign-ins.
func TestSwitchingMCPOffKeepsTheSignIns(t *testing.T) {
	as := newFakeAS(t)
	f := newFixture(t, nil, oauthServer("remote", as.url()))
	f.signIn("remote")
	f.mu.Lock()
	f.off = true
	f.mu.Unlock()
	f.pool.Changed([]string{"remote"})
	f.mu.Lock()
	f.off = false
	f.mu.Unlock()
	f.pool.Changed([]string{"remote"})
	if f.storedToken("remote") == "" {
		t.Fatal("switching MCP off forgot the sign-in")
	}
	if res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "remote__echo", map[string]string{"text": "x"}); resultText(res) != "echo: x" {
		t.Fatalf("answered %q", resultText(res))
	}
}

// Testing a draft of a signed-in server at another address never sends
// the stored token there and leaves the sign-in as it is.
func TestTestingADraftAtAnotherAddressUsesNoSignIn(t *testing.T) {
	as := newFakeAS(t)
	f := newFixture(t, nil, oauthServer("remote", as.url()))
	f.signIn("remote")
	token := f.storedToken("remote")
	rec, other := foreignHost(t)
	res, err := f.pool.Test(context.Background(), oauthServer("remote", other))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "needs a sign-in") {
		t.Fatalf("test result %+v", res)
	}
	if got := bearers(rec); len(got) != 0 {
		t.Fatalf("the draft's address was sent %v", got)
	}
	if f.storedToken("remote") != token {
		t.Fatal("the test changed the stored sign-in")
	}
	// The same draft as the saved server uses its sign-in.
	res, err = f.pool.Test(context.Background(), oauthServer("remote", as.url()))
	if err != nil || res.Error != "" || !slices.Contains(res.Tools, "remote__echo") {
		t.Fatalf("test of the saved settings: %+v %v", res, err)
	}
}
