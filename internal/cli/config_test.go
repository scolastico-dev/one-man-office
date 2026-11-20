package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigSetGetList(t *testing.T) {
	isolate(t)
	mustExecute(t, "config", "set", "port", "9000")
	if out := strings.TrimSpace(mustExecute(t, "config", "get", "port")); out != "9000" {
		t.Fatalf("get port = %q", out)
	}
	list := mustExecute(t, "config", "list")
	if n := len(strings.Split(strings.TrimSpace(list), "\n")); n != 12 {
		t.Fatalf("list has %d lines:\n%s", n, list)
	}
	mustExecute(t, "config", "set", "basic_auth", "me:secret")
	list = mustExecute(t, "config", "list")
	if !strings.Contains(list, "basic_auth: ***") || strings.Contains(list, "secret") {
		t.Fatalf("basic_auth not masked:\n%s", list)
	}
	mustExecute(t, "config", "unset", "port")
	if out := strings.TrimSpace(mustExecute(t, "config", "get", "port")); out != "8090" {
		t.Fatalf("port after unset = %q", out)
	}
	if _, err := execute(t.Context(), "config", "set", "port", "0"); err == nil {
		t.Fatal("port 0 accepted")
	}
	if _, err := execute(t.Context(), "config", "get", "nope"); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestConfigSetWhileRunningSaysNextStart(t *testing.T) {
	isolate(t)
	startRoot(t)
	out := mustExecute(t, "config", "set", "origin_check", "false")
	if !strings.Contains(out, "next start") {
		t.Fatalf("output: %q", out)
	}
	out = mustExecute(t, "config", "unset", "origin_check")
	if !strings.Contains(out, "next start") {
		t.Fatalf("unset output: %q", out)
	}
}

func TestConfigRotateToken(t *testing.T) {
	isolate(t)
	before := mustExecute(t, "config", "rotate-token")
	old := tokenFile(t)
	out := mustExecute(t, "config", "rotate-token")
	m := fragment.FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, "http://omo.localhost:") {
		t.Fatalf("output: %q", out)
	}
	if m[1] == old || m[1] != tokenFile(t) || !strings.Contains(before, old) {
		t.Fatalf("token not rotated: printed %s, old %s, file %s", m[1], old, tokenFile(t))
	}
}

func TestConfigRotateTokenWhileRunning(t *testing.T) {
	isolate(t)
	startRoot(t)
	out := mustExecute(t, "config", "rotate-token")
	if !strings.Contains(out, "next start") || !strings.Contains(out, tokenFile(t)) {
		t.Fatalf("output: %q", out)
	}
}

func TestConfigAllowedHosts(t *testing.T) {
	isolate(t)
	mustExecute(t, "config", "set", "allowed_hosts", "a.example,b.example:8443")
	if out := strings.TrimSpace(mustExecute(t, "config", "get", "allowed_hosts")); out != "a.example,b.example:8443" {
		t.Fatalf("get = %q", out)
	}
	if list := mustExecute(t, "config", "list"); !strings.Contains(list, "allowed_hosts: a.example,b.example:8443\n") {
		t.Fatalf("list:\n%s", list)
	}
	if _, err := execute(t.Context(), "config", "set", "allowed_hosts", "*.example"); err == nil {
		t.Fatal("wildcard accepted")
	}
	mustExecute(t, "config", "unset", "allowed_hosts")
	if out := strings.TrimSpace(mustExecute(t, "config", "get", "allowed_hosts")); out != "" {
		t.Fatalf("after unset = %q", out)
	}
}

func TestConfigGetPrintsBasicAuthAndSaysSo(t *testing.T) {
	isolate(t)
	mustExecute(t, "config", "set", "basic_auth", "me:secret")
	if out := strings.TrimSpace(mustExecute(t, "config", "get", "basic_auth")); out != "me:secret" {
		t.Fatalf("get basic_auth = %q", out)
	}
	if list := mustExecute(t, "config", "list"); strings.Contains(list, "secret") {
		t.Fatalf("list leaks basic_auth:\n%s", list)
	}
	help := mustExecute(t, "config", "get", "--help")
	if !strings.Contains(help, "get prints basic_auth in clear text") {
		t.Fatalf("help does not state it:\n%s", help)
	}
}

func TestConfigSetAllowedHostsNotesDefaultPorts(t *testing.T) {
	isolate(t)
	out := mustExecute(t, "config", "set", "allowed_hosts", "a.example:443")
	if !strings.Contains(out, "browsers omit the default port") {
		t.Fatalf("no note:\n%s", out)
	}
	out = mustExecute(t, "config", "set", "allowed_hosts", "a.example,b.example:8443")
	if strings.Contains(out, "default port") {
		t.Fatalf("unexpected note:\n%s", out)
	}
}

func TestConfigPublicURL(t *testing.T) {
	home := isolate(t)
	if out := strings.TrimSpace(mustExecute(t, "config", "get", "public_url")); out != "" {
		t.Fatalf("default public_url = %q", out)
	}
	out := mustExecute(t, "config", "set", "public_url", "https://Omo.Example.com")
	if !strings.Contains(out, "advertises https://omo.example.com/ and keeps listening on 127.0.0.1:8090") {
		t.Fatalf("set output: %q", out)
	}
	if out := strings.TrimSpace(mustExecute(t, "config", "get", "public_url")); out != "https://omo.example.com/" {
		t.Fatalf("get = %q", out)
	}
	if list := mustExecute(t, "config", "list"); !strings.Contains(list, "public_url: https://omo.example.com/\n") {
		t.Fatalf("list:\n%s", list)
	}
	// The login token is printed on the advertised address.
	if out := mustExecute(t, "config", "rotate-token"); !strings.Contains(out, "omo: https://omo.example.com/#") {
		t.Fatalf("rotate-token: %q", out)
	}
	for _, bad := range []string{"omo.example.com", "https://omo.example.com/omo", "https://u:p@omo.example.com/", "https://omo.example.com/?x=1", "https://omo.example.com/#t", "ftp://omo.example.com"} {
		if _, err := execute(t.Context(), "config", "set", "public_url", bad); err == nil || !strings.Contains(err.Error(), "public_url") {
			t.Errorf("%q: error %v", bad, err)
		}
	}
	if out := strings.TrimSpace(mustExecute(t, "config", "get", "public_url")); out != "https://omo.example.com/" {
		t.Fatalf("a refused value changed the option: %q", out)
	}
	mustExecute(t, "config", "unset", "public_url")
	if out := strings.TrimSpace(mustExecute(t, "config", "get", "public_url")); out != "" {
		t.Fatalf("after unset = %q", out)
	}

	// A hand-edited invalid value is reported, as for the other options.
	if err := os.WriteFile(filepath.Join(home, "server.yaml"), []byte("public_url: https://omo.example.com/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t.Context(), "config", "get", "public_url"); err == nil || !strings.Contains(err.Error(), "public_url") {
		t.Fatalf("hand-edited value: %v", err)
	}
	if _, err := execute(t.Context()); err == nil || !strings.Contains(err.Error(), "public_url") {
		t.Fatalf("start with a hand-edited value: %v", err)
	}
}

func TestConfigProxySubdomains(t *testing.T) {
	isolate(t)
	for _, kv := range [][2]string{{"proxy_label_prefix", "dev-"}, {"proxy_label_suffix", "-user123"}, {"proxy_base_host", "Omo.Localhost"}} {
		mustExecute(t, "config", "set", "--", kv[0], kv[1])
		want := strings.ToLower(kv[1])
		if got := strings.TrimSpace(mustExecute(t, "config", "get", kv[0])); got != want {
			t.Fatalf("%s: %s", kv[0], got)
		}
		if got := mustExecute(t, "config", "list"); !strings.Contains(got, kv[0]+": "+want+"\n") {
			t.Fatal(got)
		}
	}
	if _, err := execute(t.Context(), "config", "set", "proxy_base_host", "*.example"); err == nil {
		t.Fatal("wildcard accepted")
	}
	for _, key := range []string{"proxy_base_host", "proxy_label_prefix", "proxy_label_suffix"} {
		mustExecute(t, "config", "unset", key)
		if got := strings.TrimSpace(mustExecute(t, "config", "get", key)); got != "" {
			t.Fatal(got)
		}
	}
}
