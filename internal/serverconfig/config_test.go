package serverconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func cfgPath(t *testing.T) string {
	t.Setenv("OMO_HOME", t.TempDir())
	return filepath.Join(t.TempDir(), "server.yaml")
}

func TestServerConfigSetValidates(t *testing.T) {
	path := cfgPath(t)
	if _, err := Set(path, "port", "0"); err == nil {
		t.Fatal("port 0 accepted")
	}
	if _, err := Set(path, "auth", "basic"); err == nil {
		t.Fatal("auth basic without basic_auth accepted")
	}
	if _, err := Set(path, "basic_auth", "a:b"); err != nil {
		t.Fatal(err)
	}
	c, err := Set(path, "auth", "basic")
	if err != nil || c.Auth != "basic" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Set(path, "nope", "x"); err == nil {
		t.Fatal("unknown key accepted")
	}
	if c, err := Unset(path, "port"); err != nil || c.Port != 8090 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestServerConfigDefaultsWhenMissing(t *testing.T) {
	c, err := Load(cfgPath(t))
	if err != nil || !reflect.DeepEqual(c, Defaults()) || c.Listen() != "127.0.0.1:8090" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestServerConfigFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits")
	}
	path := cfgPath(t)
	if _, err := Set(path, "port", "9000"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestServerConfigRejectsUnknownKey(t *testing.T) {
	path := cfgPath(t)
	if err := os.WriteFile(path, []byte("listen: x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestTokenCreatedOnceAndReused(t *testing.T) {
	path := cfgPath(t)
	a, err := LoadOrCreateToken(path)
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a) {
		t.Fatalf("%q %v", a, err)
	}
	b, err := LoadOrCreateToken(path)
	if err != nil || a != b {
		t.Fatalf("%q != %q (%v)", a, b, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
			t.Fatalf("mode = %v", info.Mode().Perm())
		}
	}
}

func TestRotateTokenReplacesValue(t *testing.T) {
	path := cfgPath(t)
	old, _ := LoadOrCreateToken(path)
	n, err := RotateToken(path)
	if err != nil || n == old {
		t.Fatalf("%q %q %v", old, n, err)
	}
	if got, _ := LoadOrCreateToken(path); got != n {
		t.Fatalf("got %q want %q", got, n)
	}
}

func TestMalformedTokenFileIsAnError(t *testing.T) {
	path := cfgPath(t)
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateToken(path); err == nil || !strings.Contains(err.Error(), "rotate-token") {
		t.Fatalf("err = %v", err)
	}
}

func TestAllowedHostsSetGetUnset(t *testing.T) {
	path := cfgPath(t)
	c, err := Set(path, "allowed_hosts", "a.example, b.example:8443")
	if err != nil || strings.Join(c.AllowedHosts, "|") != "a.example|b.example:8443" {
		t.Fatalf("%+v %v", c, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "allowed_hosts:\n    - a.example\n    - b.example:8443") {
		t.Fatalf("not a YAML list:\n%s", raw)
	}
	loaded, err := Load(path)
	if err != nil || len(loaded.AllowedHosts) != 2 || FormatAllowedHosts(loaded.AllowedHosts) != "a.example,b.example:8443" {
		t.Fatalf("%+v %v", loaded, err)
	}
	c, err = Unset(path, "allowed_hosts")
	if err != nil || len(c.AllowedHosts) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
	if c, err = Set(path, "allowed_hosts", "x.example"); err != nil {
		t.Fatal(err)
	}
	if c, err = Set(path, "allowed_hosts", ""); err != nil || len(c.AllowedHosts) != 0 {
		t.Fatalf("empty value: %+v %v", c, err)
	}
	found := false
	for _, k := range Keys() {
		found = found || k == "allowed_hosts"
	}
	if !found {
		t.Fatal("allowed_hosts missing from Keys")
	}
}

func TestAllowedHostsValidation(t *testing.T) {
	for _, ok := range []string{"a", "a.example", "A-b.example", "a.example:1", "a.example:65535", "10.0.0.1", "10.0.0.1:80", "::1", "[::1]", "[::1]:8080", "xn--bcher-kva.example"} {
		if err := ValidateAllowedHost(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", " ", "http://a.example", "a.example/", "a.example/x", "*.example", "a.*", "a.example:", "a.example:0", "a.example:65536", "a.example:x", "a.example:-1", "a.example:08", "a.example:1:2", "-a.example", "a-.example", "a..example", ".a.example", "a b", "a.example?x", "user@a.example", "[a.example]:80", "[::1", "a_b.example"} {
		if err := ValidateAllowedHost(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	path := cfgPath(t)
	for _, v := range []string{"a.example,,b.example", "a.example,*.b", "https://a.example"} {
		if _, err := Set(path, "allowed_hosts", v); err == nil {
			t.Errorf("Set accepted %q", v)
		}
	}
	if err := os.WriteFile(path, []byte("allowed_hosts:\n  - \"*.example\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a wildcard entry")
	}
}

func TestAllowedHostsNote(t *testing.T) {
	for _, e := range []string{"a.example:443", "a.example:80", "[2001:db8::1]:443"} {
		if err := ValidateAllowedHost(e); err != nil {
			t.Errorf("%q must stay valid: %v", e, err)
		}
		if n := AllowedHostsNote([]string{"b.example", e}); !strings.Contains(n, "browsers omit the default port") || strings.Contains(n, "\n") {
			t.Errorf("%q: note %q", e, n)
		}
	}
	if n := AllowedHostsNote([]string{"a.example", "b.example:8443", "10.0.0.1"}); n != "" {
		t.Errorf("unexpected note %q", n)
	}
}
