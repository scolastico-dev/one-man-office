package cli

import (
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
	if n := len(strings.Split(strings.TrimSpace(list), "\n")); n != 6 {
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
