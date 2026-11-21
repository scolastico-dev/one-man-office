package serverconfig

import (
	"path/filepath"
	"testing"
)

func TestRelayConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml")
	c, err := Set(path, "relay_url", "https://RELAY.example:443")
	if err != nil {
		t.Fatal(err)
	}
	if c.RelayURL != "https://relay.example" || c.RelayEnabled {
		t.Fatalf("%+v", c)
	}
	c, err = Set(path, "relay_enabled", "true")
	if err != nil || !c.RelayEnabled {
		t.Fatal(c, err)
	}
	for _, bad := range []string{"http://remote.example", "https://a.example/", "https://user:secret@relay.example", "https://a.example?secret=1", "https://é.example"} {
		if _, err := Set(path, "relay_url", bad); err == nil {
			t.Fatal("accepted bad origin")
		}
	}
	if _, err := Set(path, "relay_enabled", "perhaps"); err == nil {
		t.Fatal("accepted bad boolean")
	}
	c, err = Unset(path, "relay_enabled")
	if err != nil || c.RelayEnabled {
		t.Fatal(c, err)
	}
	c, err = Unset(path, "relay_url")
	if err != nil || c.RelayURL != "" {
		t.Fatal(c, err)
	}
}
