package mcprelay

import (
	"context"
	"strings"
	"testing"
)

// The addresses an upstream names in its sign-in metadata are reached only
// when they are public or on the host of the server's own address: here
// the server is configured as localhost and names its authorization
// server on 127.0.0.1, which omo refuses to connect to.
func TestSignInMetadataCannotSendOmoToPrivateAddresses(t *testing.T) {
	as := newFakeAS(t)
	local := strings.Replace(as.url(), "127.0.0.1", "localhost", 1)
	f := newFixture(t, nil, oauthServer("remote", local))
	if _, err := f.pool.Connect(context.Background(), "remote"); err == nil {
		t.Fatal("the sign-in started")
	}
	as.mu.Lock()
	defer as.mu.Unlock()
	for _, r := range as.requests {
		if strings.HasPrefix(r, "127.0.0.1") {
			t.Fatalf("omo sent a request to the private address the server named: %v", as.requests)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	for _, c := range []struct {
		from, to string
		want     bool
	}{
		{"https://a.example/x", "https://a.example/y", true},
		{"https://a.example/x", "https://A.example:443/y", true},
		{"http://a.example/x", "https://a.example/y", true},
		{"https://a.example/x", "http://a.example/y", false},
		{"https://a.example/x", "https://b.example/y", false},
		{"https://a.example/x", "https://a.example:8443/y", false},
		{"http://localhost:1/x", "http://localhost:2/y", false},
	} {
		if got := sameOrigin(mustParse(t, c.from), mustParse(t, c.to)); got != c.want {
			t.Errorf("sameOrigin(%s, %s) = %v", c.from, c.to, got)
		}
	}
}
