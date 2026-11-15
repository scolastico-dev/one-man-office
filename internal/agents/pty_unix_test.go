//go:build !windows

// These tests run real processes on real PTYs and use Unix tools (cat) and
// signals, so they are built for Unix only.

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

const helperEnv = "OMO_AGENTS_TEST_HELPER"

// TestMain lets the test binary stand in for an agent CLI: started with
// OMO_AGENTS_TEST_HELPER set, it runs that helper instead of the tests.
func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "parent":
		// An agent that starts a long-running child, like a CLI running a
		// build in the background.
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := child.Start(); err != nil {
			fmt.Println("START FAILED", err)
			os.Exit(1)
		}
		fmt.Printf("CHILD %d END\n", child.Process.Pid)
		select {}
	case "sleep":
		select {}
	case "printid":
		// An agent that prints its id: whole, split across two writes (the
		// second only once the test saw the first), and many times.
		id := os.Getenv("OMO_AGENT_ID")
		os.Stdout.WriteString("WHOLE " + id + "\n")
		os.Stdout.WriteString("HALF " + id[:len(id)/2])
		for {
			if _, err := os.Stat(os.Getenv("OMO_TEST_GO_FILE")); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		os.Stdout.WriteString(id[len(id)/2:] + "\n")
		os.Stdout.WriteString(strings.Repeat(id+" ", 40) + "\nPRINTED-ALL\n")
		select {}
	case "identity":
		ok := os.Getenv("OMO_AGENT_ID") != "" && os.Getenv("OMO_AGENT_ID") == os.Getenv("OMO_TEST_EXPECT_ID")
		for _, arg := range os.Args {
			if strings.Contains(arg, os.Getenv("OMO_AGENT_ID")) {
				ok = false
			}
		}
		if _, leaked := os.LookupEnv("OMO_CONTROL_TOKEN"); leaked {
			ok = false
		}
		fmt.Printf("IDENTITY %v\n", ok)
		select {}
	}
	os.Exit(2)
}

func testSpec(t *testing.T, command string, args ...string) Spec {
	t.Helper()
	dir := t.TempDir()
	return Spec{
		SessionID: testSessionID,
		Name:      "developer-ada",
		CLI:       api.CLICustom,
		Command:   command,
		Args:      args,
		Dir:       dir,
		LogPath:   filepath.Join(t.TempDir(), "logs", "developer-ada.log"),
		Socket:    filepath.Join(dir, "omo.sock"),
	}
}

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("OMO_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GEMINI_CLI_HOME", t.TempDir())
}

func start(t *testing.T, r *PTYRunner, spec Spec) *ptyAgent {
	t.Helper()
	a, err := r.Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = a.Kill()
		select {
		case <-a.Done():
		case <-time.After(10 * time.Second):
			t.Error("agent still running after the test")
		}
	})
	return a.(*ptyAgent)
}

func waitScreen(t *testing.T, a *ptyAgent, want string) string {
	t.Helper()
	var screen string
	waitUntil(t, 10*time.Second, func() bool {
		screen = a.sess.Screen()
		return strings.Contains(screen, want)
	})
	return screen
}

func TestStartPromptResentUntilReady(t *testing.T) {
	isolate(t)
	const token = "omo-start-token"
	var agent atomic.Pointer[ptyAgent]
	spec := testSpec(t, "cat")
	spec.StartPrompt = token
	spec.Delivery = Delivery{Inject: true, Delay: 0, RetryWait: 10 * time.Millisecond, Retries: 3}
	spec.AwaitingReady = func() bool {
		a := agent.Load()
		return a == nil || !strings.Contains(a.sess.Screen(), token)
	}
	a := start(t, NewPTYRunner(), spec)
	agent.Store(a)
	select {
	case <-a.delivery:
	case <-time.After(10 * time.Second):
		t.Fatal("start-prompt delivery did not finish")
	}
	// The terminal echoes the typed line and cat prints it back: one
	// delivery shows the token twice.
	if n := strings.Count(a.sess.Screen(), token); n < 1 || n > 2 {
		t.Fatalf("token on screen %d times, want one delivery:\n%s", n, a.sess.Screen())
	}
}

func TestTerminalReceivesAgentOutput(t *testing.T) {
	isolate(t)
	a := start(t, NewPTYRunner(), testSpec(t, "cat"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.Terminal().ServeWebSocket(r.Context(), w, r, nil)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{Subprotocols: []string{"omo"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := a.SendPrompt("hello"); err != nil {
		t.Fatal(err)
	}
	var seen strings.Builder
	for !strings.Contains(seen.String(), "hello") {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("websocket read: %v (seen %q)", err, seen.String())
		}
		if kind == websocket.MessageBinary {
			seen.Write(data)
		}
	}

	// Input from the viewer reaches the agent and counts as a person typing.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("typed-by-user")); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, a, "typed-by-user")
	if a.notes.holdBack(time.Time{}) <= 0 {
		t.Fatal("viewer input did not hold notifications back")
	}
}

func TestNotifyTypesOneLineIntoTheAgent(t *testing.T) {
	isolate(t)
	r := &PTYRunner{Debounce: -1}
	a := start(t, r, testSpec(t, "cat"))
	a.Notify("omo: first\nsecond")
	// cat prints the submitted line back once the terminal echoed it.
	waitUntil(t, 10*time.Second, func() bool { return strings.Count(a.sess.Screen(), "omo: first second") == 2 })
}

func TestClaudeTrustWrittenForWorkdir(t *testing.T) {
	isolate(t)
	home := os.Getenv("HOME")
	config := os.Getenv("CLAUDE_CONFIG_DIR")
	spec := testSpec(t, "cat")
	spec.CLI = api.CLIClaude
	a := start(t, NewPTYRunner(), spec)
	if w := a.Warnings(); len(w) != 0 {
		t.Fatalf("warnings: %v", w)
	}
	raw, err := os.ReadFile(filepath.Join(config, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Projects[spec.Dir].Trusted {
		t.Fatalf("%s not trusted: %s", spec.Dir, raw)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("trust written below HOME although CLAUDE_CONFIG_DIR is set: %v", err)
	}

	// An account folder in the profile selects that account's file instead.
	account := t.TempDir()
	other := testSpec(t, "cat")
	other.CLI = api.CLIClaude
	other.AccountDir = account
	start(t, NewPTYRunner(), other)
	if _, err := os.Stat(filepath.Join(account, ".claude.json")); err != nil {
		t.Fatalf("account folder trust: %v", err)
	}
}

func TestClaudeTrustFailureIsAWarning(t *testing.T) {
	isolate(t)
	spec := testSpec(t, "cat")
	spec.CLI = api.CLIClaude
	bad := filepath.Join(t.TempDir(), "account")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, ".claude.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec.AccountDir = bad
	a := start(t, NewPTYRunner(), spec)
	w := a.Warnings()
	if len(w) != 1 || !strings.Contains(w[0], "trust") {
		t.Fatalf("warnings = %v", w)
	}
	if strings.Contains(strings.Join(w, " "), testSessionID) {
		t.Fatal("warning contains the session id")
	}
}

func TestKillEndsAgent(t *testing.T) {
	isolate(t)
	a := start(t, NewPTYRunner(), testSpec(t, "cat"))
	if err := a.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("agent still running 5s after Kill")
	}
	if a.ExitErr() == nil {
		t.Fatal("a killed agent reports a clean exit")
	}
	if err := a.Kill(); err != nil {
		t.Fatalf("second Kill: %v", err)
	}
	select {
	case <-a.Terminal().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not end with its agent")
	}
}

func TestKillEndsChildProcesses(t *testing.T) {
	isolate(t)
	spec := testSpec(t, os.Args[0])
	spec.ProfileEnv = map[string]string{helperEnv: "parent"}
	a := start(t, NewPTYRunner(), spec)
	screen := waitScreen(t, a, " END")
	_, after, _ := strings.Cut(screen, "CHILD ")
	pid, err := strconv.Atoi(strings.Fields(after)[0])
	if err != nil {
		t.Fatalf("no child pid on screen:\n%s", screen)
	}
	if err := a.Kill(); err != nil {
		t.Fatal(err)
	}
	<-a.Done()
	waitUntil(t, 10*time.Second, func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	})
}

func TestSessionIDOnlyInEnvironment(t *testing.T) {
	isolate(t)
	t.Setenv("OMO_AGENT_ID", "inherited-identity")
	t.Setenv("OMO_CONTROL_TOKEN", "parent-secret")
	spec := testSpec(t, os.Args[0])
	spec.ProfileEnv = map[string]string{helperEnv: "identity", "OMO_TEST_EXPECT_ID": testSessionID, "OMO_AGENT_ID": "profile-identity"}
	a := start(t, NewPTYRunner(), spec)
	waitScreen(t, a, "IDENTITY true")

	bad := testSpec(t, "cat", "--session", testSessionID)
	if _, err := NewPTYRunner().Start(bad); !errors.Is(err, ErrSessionIDExposed) {
		t.Fatalf("spec with the id in an argument: %v", err)
	}
}

func TestStartFailureLeavesNothingRunning(t *testing.T) {
	isolate(t)
	spec := testSpec(t, filepath.Join(t.TempDir(), "no-such-cli"))
	_, err := NewPTYRunner().Start(spec)
	if err == nil {
		t.Fatal("start of a missing command succeeded")
	}
	if strings.Contains(err.Error(), testSessionID) {
		t.Fatalf("error contains the session id: %v", err)
	}
}

func TestRegistryTracksRunningAgents(t *testing.T) {
	isolate(t)
	reg := NewRegistry()
	a := start(t, NewPTYRunner(), testSpec(t, "cat"))
	if err := reg.Add(a); err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(a); !errors.Is(err, ErrDuplicateSession) {
		t.Fatalf("second Add: %v", err)
	}
	if got, ok := reg.Get(testSessionID); !ok || got != a {
		t.Fatal("registry does not return the agent")
	}
	if term, ok := reg.Terminal(testSessionID); !ok || term != a.Terminal() {
		t.Fatal("registry does not return the terminal")
	}
	if !reg.KillAll(5 * time.Second) {
		t.Fatal("KillAll timed out")
	}
	waitUntil(t, 5*time.Second, func() bool { _, ok := reg.Get(testSessionID); return !ok })
}

func TestStartFailsWhenAnEnvironmentCommandHangs(t *testing.T) {
	isolate(t)
	spec := testSpec(t, "cat")
	spec.AgentEnv = map[string]string{"VAULT_TOKEN": "`sleep 60`"}
	started := time.Now()
	_, err := (&PTYRunner{EnvTimeout: 200 * time.Millisecond}).Start(spec)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("Start took %v", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "VAULT_TOKEN") || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout naming VAULT_TOKEN", err)
	}
}

func TestSessionIDRedactedFromTranscriptAndViewers(t *testing.T) {
	isolate(t)
	goFile := filepath.Join(t.TempDir(), "go")
	spec := testSpec(t, os.Args[0])
	spec.ProfileEnv = map[string]string{helperEnv: "printid", "OMO_TEST_GO_FILE": goFile}
	a := start(t, NewPTYRunner(), spec)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.Terminal().ServeWebSocket(r.Context(), w, r, nil)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{Subprotocols: []string{"omo"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	waitScreen(t, a, "HALF")
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var seen strings.Builder
	for !strings.Contains(seen.String(), "PRINTED-ALL") {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("websocket read: %v", err)
		}
		if kind == websocket.MessageBinary {
			seen.Write(data)
		}
	}
	if strings.Contains(seen.String(), testSessionID) {
		t.Error("the viewer stream contains the session id")
	}
	if !strings.Contains(seen.String(), "WHOLE [redacted]") {
		t.Errorf("viewer stream lacks the placeholder: %q", seen.String())
	}
	if strings.Contains(a.sess.Screen(), testSessionID) {
		t.Error("the screen contains the session id")
	}
	tail := strings.Join(a.Tail(100), "\n")
	if strings.Contains(tail, testSessionID) || !strings.Contains(tail, "PRINTED-ALL") {
		t.Errorf("Tail leaks the id or misses output:\n%s", tail)
	}
	_ = a.Kill()
	<-a.Done()
	raw, err := os.ReadFile(spec.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testSessionID) {
		t.Error("the log file contains the session id")
	}
	if !strings.Contains(string(raw), "HALF [redacted]") {
		t.Errorf("split id not redacted as one in the log:\n%s", raw)
	}
}

func TestTerminalIsNotKeyedBySessionID(t *testing.T) {
	isolate(t)
	reg := NewRegistry()
	a := start(t, NewPTYRunner(), testSpec(t, "cat"))
	b := start(t, NewPTYRunner(), func() Spec { s := testSpec(t, "cat"); s.SessionID = "fedcba9876543210fedcba9876543210"; return s }())
	if err := reg.Add(a); err != nil {
		t.Fatal(err)
	}
	if a.TerminalID() == "" || a.TerminalID() == b.TerminalID() {
		t.Fatalf("terminal ids %q and %q", a.TerminalID(), b.TerminalID())
	}
	if a.Terminal().Info().ID != a.TerminalID() {
		t.Fatal("terminal Info.ID is not the terminal id")
	}
	if term, ok := reg.TerminalByID(a.TerminalID()); !ok || term != a.Terminal() {
		t.Fatal("lookup by terminal id failed")
	}
	if _, ok := reg.TerminalByID(testSessionID); ok {
		t.Fatal("a session id finds a terminal by terminal id")
	}
	if term, ok := reg.Terminal(testSessionID); !ok || term != a.Terminal() {
		t.Fatal("lookup by session id failed")
	}

	if err := a.SendPrompt("hello"); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, a, "hello")
	info, err := json.Marshal(a.Terminal().Info())
	if err != nil {
		t.Fatal(err)
	}
	exposed := []string{a.TerminalID(), string(info), strings.Join(a.Warnings(), "\n"), strings.Join(a.Tail(50), "\n"), a.LastOutput().String()}
	_ = a.Kill()
	<-a.Done()
	if err := a.ExitErr(); err != nil {
		exposed = append(exposed, err.Error())
	}
	info, _ = json.Marshal(a.Terminal().Info())
	exposed = append(exposed, string(info))
	for _, value := range exposed {
		if strings.Contains(value, testSessionID) {
			t.Errorf("an accessor exposes the session id: %q", value)
		}
	}
	waitUntil(t, 5*time.Second, func() bool { _, ok := reg.TerminalByID(a.TerminalID()); return !ok })
}
