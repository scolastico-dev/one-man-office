package userterm

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
)

func envValue(env []string, key string) (string, bool) {
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

func TestShellEnvHasNoAgentIdentity(t *testing.T) {
	base := []string{
		"PATH=/usr/bin", "HOME=/home/u", "OMO_AGENT_ID=secret-session", "OMO_FAKEAGENT=1",
		"OMO_FAKE_SCRIPT_X=y", "OMO_CONTROL_TOKEN=tok", "OMO_SOCKET=/old", "TERM=dumb", "COLORTERM=no", "COLORFGBG=0;15",
	}
	env := ShellEnv(base, "/run/omo.sock")
	for _, key := range []string{"OMO_AGENT_ID", "OMO_FAKEAGENT", "OMO_FAKE_SCRIPT_X", "OMO_CONTROL_TOKEN", "COLORFGBG"} {
		if _, ok := envValue(env, key); ok {
			t.Errorf("%s is set", key)
		}
	}
	for _, e := range env {
		if strings.Contains(e, "secret-session") || strings.Contains(e, "tok") && strings.HasPrefix(e, "OMO_CONTROL") {
			t.Errorf("identity leaked: %q", e)
		}
	}
	for key, want := range map[string]string{"PATH": "/usr/bin", "HOME": "/home/u", "OMO_SOCKET": "/run/omo.sock", "TERM": "xterm-256color", "COLORTERM": "truecolor"} {
		if got, _ := envValue(env, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "TERM=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("TERM set %d times", count)
	}
	if _, ok := envValue(ShellEnv(base, ""), "OMO_SOCKET"); ok {
		t.Error("OMO_SOCKET set without a socket")
	}
}

// A user shell is detached from the IDE or terminal session omo was started
// from, like an agent, but keeps the ordinary environment.
func TestShellEnvIsDetachedFromTheIDE(t *testing.T) {
	childenvtest.Set(t)
	t.Setenv("OMO_USERTERM_ORDINARY", "kept")
	env := New(Options{}).o.Env()
	for _, e := range env {
		name, value, _ := strings.Cut(e, "=")
		if childenv.Detached(name, value) {
			t.Errorf("%s reached a user shell", name)
		}
	}
	if got, _ := envValue(env, "OMO_USERTERM_ORDINARY"); got != "kept" {
		t.Errorf("ordinary variable = %q", got)
	}
}

func TestStripIdentityRemovesIdentityFromAnyEnv(t *testing.T) {
	got := stripIdentity([]string{"A=1", "omo_agent_id=x", "OMO_FAKE_SCRIPT_1=y", "OMO_SOCKET=s"})
	if !slices.Equal(got, []string{"A=1", "OMO_SOCKET=s"}) {
		t.Fatalf("got %v", got)
	}
}

func TestOpenRefusesBadDirectory(t *testing.T) {
	m := New(Options{})
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"", filepath.Join(dir, "missing"), file} {
		if _, err := m.Open(Spec{Dir: d}); err == nil {
			t.Errorf("Open(%q) succeeded", d)
		}
	}
	if len(m.List()) != 0 {
		t.Error("a refused open left a shell")
	}
}

func TestIDsAreUnguessable(t *testing.T) {
	a, _ := newID()
	b, _ := newID()
	if len(a) != 32 || a == b {
		t.Fatalf("ids %q %q", a, b)
	}
}
