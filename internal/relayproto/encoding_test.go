package relayproto

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

// These cases guard the bytes consumed by independent native clients, not JSON layout.
func TestEncoding(t *testing.T) {
	if got := hex.EncodeToString(EncodeFields([]byte("a"), nil, []byte{0, 255})); got != "0000000161000000000000000200ff" {
		t.Fatal(got)
	}
	for _, raw := range []string{`"AA=="`, `"AB"`, `"+w"`, `"AA\n"`, `null`, `42`} {
		var b Bytes
		if json.Unmarshal([]byte(raw), &b) == nil {
			t.Errorf("accepted bytes %s", raw)
		}
	}
	var b Bytes
	if err := json.Unmarshal([]byte(`"AP8"`), &b); err != nil || !bytes.Equal(b, []byte{0, 255}) {
		t.Fatal(b, err)
	}
	for _, raw := range []string{`"01"`, `"+1"`, `"-1"`, `"1.0"`, `1`, `null`, `"18446744073709551616"`} {
		var n Counter
		if json.Unmarshal([]byte(raw), &n) == nil {
			t.Errorf("accepted counter %s", raw)
		}
	}
	var n Counter
	if err := json.Unmarshal([]byte(`"18446744073709551615"`), &n); err != nil || uint64(n) != ^uint64(0) {
		t.Fatal(n, err)
	}
}

func TestIdentity(t *testing.T) {
	seed := make([]byte, 64)
	seed[0] = 1
	id, err := ImportIdentity(seed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(id.Seed(), seed) || len(id.PublicKey()) != 1184 {
		t.Fatal("identity encoding")
	}
	if err := ValidatePublicKey(id.PublicKey()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{nil, make([]byte, 63), make([]byte, 65)} {
		if _, err := ImportIdentity(bad); err == nil {
			t.Fatal("accepted seed")
		}
	}
	for _, bad := range [][]byte{nil, make([]byte, 1183), bytes.Repeat([]byte{255}, 1184)} {
		if ValidatePublicKey(bad) == nil {
			t.Fatal("accepted public key")
		}
	}
	h, _ := Fingerprint("host", id.PublicKey())
	u, _ := Fingerprint("ui", id.PublicKey())
	if len(h) != 96 || h == u {
		t.Fatal("role separation")
	}
}

func TestRoute(t *testing.T) {
	r, err := NewRoute("https://EXAMPLE.com:443", "office", false)
	if err != nil || r.Origin != "https://example.com" {
		t.Fatal(r, err)
	}
	for _, origin := range []string{"https://u:p@example.com", "https://example.com/", "https://example.com?q=x", "https://example.com#x", "http://example.com", "https://example.com:0", "https://example.com:abc"} {
		if _, err := NewRoute(origin, "office", false); err == nil {
			t.Errorf("accepted %s", origin)
		}
	}
	if _, err := NewRoute("http://127.0.0.1:8080", "office", true); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"", "a/b", "a b", string(bytes.Repeat([]byte{'a'}, 65))} {
		if _, err := NewRoute("https://example.com", host, false); err == nil {
			t.Fatal("accepted host id", host)
		}
	}
}

func TestZeroIdentityRejected(t *testing.T) {
	for _, id := range []*Identity{nil, {}} {
		if len(id.Seed()) != 0 || len(id.PublicKey()) != 0 {
			t.Fatal("zero identity encoded")
		}
		if _, e := NewPairHost(testRoute(), id, new(memoryPairStore), func() time.Time { return time.Unix(100, 0) }, fixedRandom(1)); e == nil {
			t.Fatal("invalid host identity")
		}
		if _, _, e := NewPairUI(testRoute(), id, fixedRandom(1)); e == nil {
			t.Fatal("invalid UI identity")
		}
		c := authContext(t, "reconnect")
		if _, e := NewHostHandshake(c, id, fixedRandom(1)); e == nil {
			t.Fatal("invalid handshake host")
		}
		if _, _, e := NewUIHandshake(c, id, fixedRandom(1)); e == nil {
			t.Fatal("invalid handshake UI")
		}
	}
}

func TestRouteHostUsesInteroperableASCIIEncoding(t *testing.T) {
	for _, origin := range []string{"https://bücher.example", "https://relay_office.example", "https://a..example", "https://-relay.example", "https://relay-.example", "https://[example.com]"} {
		if _, e := NewRoute(origin, "office", false); e == nil {
			t.Errorf("noncanonical hostname accepted: %s", origin)
		}
	}
	for _, origin := range []string{"https://xn--bcher-kva.example", "https://relay-office.example", "https://127.0.0.1", "https://[::1]"} {
		if r, e := NewRoute(origin, "office", false); e != nil || r.Origin != origin {
			t.Errorf("canonical route %s: %v %v", origin, r, e)
		}
	}
}
