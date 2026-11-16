// Package e2e drives omo end to end: a real server with its engine, the
// agent socket and the browser listener, agents that are real processes
// (the scripted fake agent, run as this test binary) and real git
// repositories. The tests assert on the board and on the repositories.
package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/fakeagent"
	"github.com/scolastico-dev/one-man-office/internal/gittest"
)

// runMarker is put into the environment of every agent this test binary
// starts, so the end of the run can find any agent process left behind.
const runMarker = "OMO_E2E_RUN"

// runID is this run's value of runMarker.
var runID string

// TestMain runs the binary as the scripted fake agent when the engine starts
// it as an agent; otherwise it isolates the process environment once for
// every test (no user or system git configuration, a temporary HOME, fake
// forge tools first on PATH), runs the tests and checks that no agent
// process outlived them.
func TestMain(m *testing.M) {
	if fakeagent.Enabled() {
		os.Exit(fakeagent.Main())
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	runID = hex.EncodeToString(b[:])
	home, err := os.MkdirTemp("", "omo-e2e-home-")
	if err != nil {
		panic(err)
	}
	gittest.Isolate()
	for k, v := range map[string]string{
		"HOME":                home,
		"XDG_CONFIG_HOME":     filepath.Join(home, ".config"),
		"CLAUDE_CONFIG_DIR":   filepath.Join(home, ".claude"),
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "Test User",
		"GIT_AUTHOR_EMAIL":    "user@example.invalid",
		"GIT_COMMITTER_NAME":  "Test User",
		"GIT_COMMITTER_EMAIL": "user@example.invalid",
		"GIT_TERMINAL_PROMPT": "0",
	} {
		os.Setenv(k, v)
	}
	for _, k := range []string{"OMO_HOME", "OMO_SOCKET", "OMO_AGENT_ID", fakeagent.EnvScript} {
		os.Unsetenv(k)
	}
	bin := filepath.Join(home, "forge-bin")
	if err := writeFakeForgeTools(bin); err != nil {
		panic(err)
	}
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	if left := leftoverAgents(); len(left) > 0 {
		fmt.Fprintf(os.Stderr, "e2e: agent processes outlived the tests: %v\n", left)
		code = 1
	}
	os.RemoveAll(home)
	os.Exit(code)
}

// ghLogVariable names the folder the fake gh logs to, a file per call;
// worlds set it in the agent environment. Without it gh logs nothing.
const ghLogVariable = "E2E_GH_LOG"

// writeFakeForgeTools writes gh, glab and tea into bin, which goes first on
// PATH: omo finds them when it looks for a forge's tool, and an export job
// runs them, never a real one, so no test reaches a forge. gh logs each call
// into a file of its own (every argument on a line "ARG <arg>", the content
// of the --body-file as "BODY<<<text>>>") and prints the address of a pull
// request of its --repo on github.com; glab and tea refuse.
func writeFakeForgeTools(bin string) error {
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	gh := `#!/bin/sh
log=/dev/null
if [ -n "$` + ghLogVariable + `" ]; then log="$` + ghLogVariable + `/call-$$.log"; fi
repo=
for a in "$@"; do
  printf 'ARG %s\n' "$a" >> "$log"
  case "$a" in
    --repo=*) repo="${a#--repo=}" ;;
    --body-file=*) printf 'BODY<<%s>>\n' "$(cat "${a#--body-file=}")" >> "$log" ;;
  esac
done
echo "https://github.com/$repo/pull/1"
`
	refuse := "#!/bin/sh\necho \"fake $0: not available in tests\" >&2\nexit 1\n"
	for name, script := range map[string]string{"gh": gh, "glab": refuse, "tea": refuse} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// leftoverAgents returns the ids of live processes that carry this run's
// marker. Agents end asynchronously after their session, so it waits up to
// 15 seconds for them. It only works where /proc exists (Linux); elsewhere
// the per-test checks of the recorded agents remain.
func leftoverAgents() []int {
	if runtime.GOOS != "linux" {
		return nil
	}
	want := []byte(runMarker + "=" + runID + "\x00")
	deadline := time.Now().Add(15 * time.Second)
	for {
		var left []int
		entries, _ := os.ReadDir("/proc")
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || pid == os.Getpid() {
				continue
			}
			env, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
			if err != nil || !bytes.Contains(env, want) {
				continue
			}
			if stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat")); err == nil && zombie(string(stat)) {
				continue
			}
			left = append(left, pid)
		}
		if len(left) == 0 || time.Now().After(deadline) {
			return left
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// zombie reports whether a /proc/<pid>/stat line is of a process that has
// exited and only waits to be reaped.
func zombie(stat string) bool {
	i := strings.LastIndexByte(stat, ')')
	return i >= 0 && i+2 < len(stat) && stat[i+2] == 'Z'
}
