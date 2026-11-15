package serverconfig

import (
	"os"
	"path/filepath"
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
	if err != nil || c != Defaults() || c.Listen() != "127.0.0.1:8090" {
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
