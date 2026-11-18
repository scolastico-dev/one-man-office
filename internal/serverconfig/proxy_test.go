package serverconfig

import (
	"os"
	"strings"
	"testing"
)

func TestProxyConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		base, prefix, suffix string
		valid                bool
	}{
		{"", "", "", true}, {"omo.localhost", "", "", true}, {"Proxy.Example", "DEV-", "-user123", true},
		{"omo.localhost", strings.Repeat("a", 31), "", true},
		{"omo.localhost", strings.Repeat("a", 32), "", false},
		{"omo.localhost", "-", "", false}, {"omo.localhost", "", "-", false},
		{"omo.localhost", "a.b", "", false}, {"omo.localhost", "", "_user", false},
		{"https://omo.example", "", "", false}, {"omo.example:8090", "", "", false},
		{"*.omo.example", "", "", false}, {"omo.example/", "", "", false},
		{"omo.example.", "", "", false}, {"127.0.0.1", "", "", false}, {"::1", "", "", false},
		{"localhost", "", "", false}, {"com", "", "", false},
		{strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 28), "", "", true},
		{strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 29), "", "", false},
		{"", "dev-", "-user", true},
	} {
		t.Run(tc.base+"/"+tc.prefix+"/"+tc.suffix, func(t *testing.T) {
			path := cfgPath(t)
			data := "proxy_base_host: '" + tc.base + "'\nproxy_label_prefix: '" + tc.prefix + "'\nproxy_label_suffix: '" + tc.suffix + "'\n"
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestProxyConfigSetUnset(t *testing.T) {
	path := cfgPath(t)
	for _, kv := range [][2]string{{"proxy_label_prefix", "Dev-"}, {"proxy_label_suffix", "-User"}, {"proxy_base_host", "Omo.Localhost"}} {
		if _, err := Set(path, kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Set(path, "proxy_label_prefix", "bad."); err == nil {
		t.Fatal("invalid label accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("invalid setting changed file")
	}
	for _, key := range []string{"proxy_base_host", "proxy_label_prefix", "proxy_label_suffix"} {
		if _, err := Unset(path, key); err != nil {
			t.Fatal(err)
		}
	}
	_, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
}

func TestProxyConfigAmbiguousBoardHost(t *testing.T) {
	for _, extra := range []string{"public_url: https://0123456789abcdef0123456789abcdef.apps.example\n", "allowed_hosts: [0123456789abcdef0123456789abcdef.apps.example]\n"} {
		path := cfgPath(t)
		if err := os.WriteFile(path, []byte("proxy_base_host: apps.example\n"+extra), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("board host overlaps the forward namespace")
		}
	}
}

func TestProxyConfigPreservesExplicitBoardAliases(t *testing.T) {
	path := cfgPath(t)
	data := "proxy_base_host: apps.example\npublic_url: https://board.apps.example\nallowed_hosts: [other.apps.example, extra.board.apps.example:8443]\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
}
