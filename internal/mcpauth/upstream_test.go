package mcpauth

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"golang.org/x/oauth2"
)

func TestUpstreamSaveLoadDelete(t *testing.T) {
	d := openDB(t)
	c := newClock()
	u := NewUpstream(d, c.now)
	ctx := context.Background()
	if _, ok, err := u.Load(ctx, "github"); ok || err != nil {
		t.Fatalf("load of an unknown server: %v %v", ok, err)
	}
	if err := u.Save(ctx, UpstreamAuth{Server: "github", Issuer: "https://as.example", ClientID: "cid",
		RedirectURI: "http://localhost:8090/oauth/callback", State: api.MCPAuthStateNeedsLogin}); err != nil {
		t.Fatal(err)
	}
	a, ok, err := u.Load(ctx, "github")
	if err != nil || !ok || a.Token != nil || string(a.Registration) != "{}" || a.State != api.MCPAuthStateNeedsLogin {
		t.Fatalf("loaded %+v %v %v", a, ok, err)
	}
	expiry := c.now().Add(time.Hour)
	want := UpstreamAuth{
		Server: "github", Issuer: "https://as.example", ClientID: "cid", ClientSecret: "csecret",
		RedirectURI:  "http://localhost:8090/oauth/callback",
		State:        api.MCPAuthStateConnected,
		Registration: json.RawMessage(`{"client_id":"cid"}`),
		Token:        &oauth2.Token{AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer", Expiry: expiry},
		Scopes:       []string{"repo", "read:org"},
	}
	if err := u.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := u.Load(ctx, "github")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !got.Token.Expiry.Equal(expiry) {
		t.Fatalf("expiry = %v", got.Token.Expiry)
	}
	got.Token.Expiry = expiry
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded\n%+v\nwant\n%+v", got, want)
	}
	// Stored as they are: the relay sends them upstream.
	if dump := dumpTable(t, d, "mcp_upstream_auth"); !contains(dump, "csecret", "|at|", "|rt|") {
		t.Fatalf("the values are not stored as they are: %s", dump)
	}
	if err := u.Delete(ctx, "github"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := u.Load(ctx, "github"); ok {
		t.Fatal("the sign-in survived its deletion")
	}
	if err := u.Delete(ctx, "github"); err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamRefusesBadRegistration(t *testing.T) {
	u := NewUpstream(openDB(t), nil)
	if err := u.Save(context.Background(), UpstreamAuth{Server: "x", Registration: json.RawMessage("{")}); err == nil {
		t.Fatal("a registration that is not JSON was stored")
	}
}

func contains(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
