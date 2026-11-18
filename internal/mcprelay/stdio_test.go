package mcprelay

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

func TestStdioToolsAreOfferedUnderTheServersName(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", nil))
	id := agent("s1", t.TempDir(), api.RoleDeveloper)
	tools, err := f.pool.Tools(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	names := toolNames(tools)
	for _, want := range []string{"fake__echo", "fake__whoami", "fake__sleep"} {
		if !slices.Contains(names, want) {
			t.Errorf("tools %v lack %s", names, want)
		}
	}
	for _, tool := range tools {
		if tool.Server != "fake" || strings.Contains(tool.Tool.Description, "Relayed by") {
			t.Errorf("tool %s: server %q, description %q", tool.Name, tool.Server, tool.Tool.Description)
		}
	}
	res := f.call(id, "fake__echo", map[string]string{"text": "hi"})
	if res.IsError || resultText(res) != "echo: hi" {
		t.Fatalf("echo answered %+v", res)
	}
	if st := f.state("fake"); st.State != api.MCPStateRunning || st.Instances != 1 || !slices.Contains(st.Tools, "fake__echo") {
		t.Fatalf("state %+v", st)
	}
	if f.pool.Generation() == 0 || f.events.count(api.KindMCPToolsChanged) == 0 {
		t.Fatal("the first listing of a server did not raise the generation")
	}
}

func TestTwoSessionsReachTheirOwnProcessInTheirOwnFolder(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", nil))
	dirA, dirB := t.TempDir(), t.TempDir()
	a := agent("session-a", dirA, api.RoleDeveloper)
	b := agent("session-b", dirB, api.RoleDeveloper)
	var wg sync.WaitGroup
	got := map[string]string{}
	var mu sync.Mutex
	for _, id := range []struct {
		name string
		dir  string
	}{{"session-a", dirA}, {"session-b", dirB}} {
		wg.Go(func() {
			for range 5 {
				res := f.call(agent(id.name, id.dir, api.RoleDeveloper), "fake__whoami", nil)
				mu.Lock()
				if prev, ok := got[id.name]; ok && prev != resultText(res) {
					t.Errorf("%s reached two processes: %s and %s", id.name, prev, resultText(res))
				}
				got[id.name] = resultText(res)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	ra, rb := got["session-a"], got["session-b"]
	if !strings.HasSuffix(ra, "cwd="+dirA) || !strings.HasSuffix(rb, "cwd="+dirB) {
		t.Fatalf("folders: a %q, b %q", ra, rb)
	}
	if pid(ra) == pid(rb) {
		t.Fatalf("both sessions reached process %s", pid(ra))
	}
	if st := f.state("fake"); st.Instances != 2 {
		t.Fatalf("instances %d, want 2", st.Instances)
	}
	f.pool.EndInstance("session-a")
	if st := f.state("fake"); st.Instances != 1 {
		t.Fatalf("after the end of session a: instances %d, want 1", st.Instances)
	}
	if res := f.call(b, "fake__whoami", nil); resultText(res) != rb {
		t.Fatalf("session b lost its process: %q", resultText(res))
	}
	// Session a's next use starts a new process; its old one is gone.
	if res := f.call(a, "fake__whoami", nil); pid(resultText(res)) == pid(ra) {
		t.Fatal("an ended session's process was reused")
	}
}

func pid(whoami string) string {
	p, _, _ := strings.Cut(whoami, " ")
	return p
}

func TestConnectionsWithoutASessionGetOnlyGlobalServersAndOneSharedProcess(t *testing.T) {
	global := stdioServer("global", nil)
	global.Global = true
	f := newFixture(t, nil, global, stdioServer("private", nil))
	tools, err := f.pool.Tools(context.Background(), userClass())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range toolNames(tools) {
		if !strings.HasPrefix(name, "global__") {
			t.Fatalf("a connection without a session sees %s", name)
		}
	}
	if len(tools) == 0 {
		t.Fatal("no global tools")
	}
	if _, err := f.pool.Call(context.Background(), userClass(), "private__echo", json.RawMessage(`{}`), nil); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("a private server's tool answered %v, want unknown tool", err)
	}
	first := resultText(f.call(userClass(), "global__whoami", nil))
	other := userClass()
	other.Instance = "another-bridge"
	other.Workdir = t.TempDir()
	second := resultText(f.call(other, "global__whoami", nil))
	if first != second {
		t.Fatalf("connections without a session reached two processes: %q, %q", first, second)
	}
	if !strings.HasSuffix(first, "cwd="+f.home) {
		t.Fatalf("the shared process runs in %q, want the home folder %s", first, f.home)
	}
}

func TestRolesSelectWhichSessionsSeeAServer(t *testing.T) {
	s := stdioServer("reviewers", nil)
	s.Roles = []string{"reviewer", "export"}
	f := newFixture(t, nil, s)
	dev := agent("dev", t.TempDir(), api.RoleDeveloper)
	if tools, _ := f.pool.Tools(context.Background(), dev); len(tools) != 0 {
		t.Fatalf("a developer sees %v", toolNames(tools))
	}
	if _, err := f.pool.Call(context.Background(), dev, "reviewers__echo", nil, nil); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("a developer's call answered %v", err)
	}
	export := agent("exp", t.TempDir(), api.RoleDeveloper)
	export.Actor.Purpose = api.PurposeExport
	if res := f.call(export, "reviewers__echo", map[string]string{"text": "x"}); res.IsError {
		t.Fatalf("an export job's call failed: %s", resultText(res))
	}
	if _, err := f.pool.Call(context.Background(), export, "reviewers__nothing", nil, nil); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("an unknown tool answered %v", err)
	}
	if _, err := f.pool.Call(context.Background(), export, "echo", nil, nil); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("a name without a server answered %v", err)
	}
}

func TestProcessGetsNoOmoVariablesAndItsOwnValues(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", map[string]string{"FAKE_VALUE": "${OMO_HOME}-x", "OMO_INJECTED": "1"}))
	res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "fake__printenv", nil)
	names := strings.Split(resultText(res), "\n")
	if !slices.Contains(names, "FAKE_VALUE") {
		t.Fatalf("FAKE_VALUE missing from %v", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "OMO_") {
			t.Fatalf("the process got %s", n)
		}
	}
}

func TestACrashedProcessRestartsOnTheNextCall(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", nil))
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	before := resultText(f.call(id, "fake__whoami", nil))
	res := f.call(id, "fake__crash", nil)
	if !res.IsError || !strings.Contains(resultText(res), "server fake is not reachable") {
		t.Fatalf("the crash answered %q", resultText(res))
	}
	eventually(t, "the crash to be noticed", func() bool { return f.state("fake").Instances == 0 })
	if st := f.state("fake"); st.LastError == "" || st.State == api.MCPStateFailed {
		t.Fatalf("after one crash: %+v", st)
	}
	after := f.call(id, "fake__whoami", nil)
	if after.IsError || pid(resultText(after)) == pid(before) {
		t.Fatalf("after the crash: %q (before %q)", resultText(after), before)
	}
	if !strings.Contains(f.logText("fake"), "ended unexpectedly") {
		t.Fatalf("the log does not mention the crash:\n%s", f.logText("fake"))
	}
}

// The crash is announced (the instance leaves the pool) only after the line
// about it is in the log: whoever sees the instance gone finds the line.
func TestACrashIsNotAnnouncedBeforeItsLogLine(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", nil))
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	f.call(id, "fake__whoami", nil)
	reached, release := make(chan struct{}), make(chan struct{})
	f.pool.mu.Lock()
	lg := f.pool.servers["fake"].log
	f.pool.mu.Unlock()
	var once sync.Once
	lg.setBeforeWrite(func(msg string) {
		if strings.Contains(msg, "ended unexpectedly") {
			once.Do(func() { close(reached) })
			<-release
		}
	})
	f.call(id, "fake__crash", nil)
	<-reached
	// The line is about to be written and is held: the crash must not be
	// published yet.
	instances := f.state("fake").Instances
	close(release)
	if instances == 0 {
		t.Fatal("the crash was announced before its log line was written")
	}
	eventually(t, "the crash to be noticed", func() bool { return f.state("fake").Instances == 0 })
	if !strings.Contains(f.logText("fake"), "ended unexpectedly") {
		t.Fatalf("the log does not mention the crash:\n%s", f.logText("fake"))
	}
}

func TestAServerThatKeepsFailingIsStoppedUntilRestarted(t *testing.T) {
	f := newFixture(t, func(t *tunables) { t.maxFailures = 3 }, stdioServer("fake", map[string]string{fakemcp.EnvMode: string(fakemcp.ModeExit)}))
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	for range 3 {
		res := f.call(id, "fake__echo", nil)
		if !res.IsError {
			t.Fatal("a server that exits at start answered")
		}
	}
	st := f.state("fake")
	if st.State != api.MCPStateFailed || !strings.Contains(st.LastError, "exited") {
		t.Fatalf("state %+v", st)
	}
	res := f.call(id, "fake__echo", nil)
	if !strings.Contains(resultText(res), "failed too often") {
		t.Fatalf("a failed server answered %q", resultText(res))
	}
	f.set(stdioServer("fake", nil))
	if err := f.pool.Restart("fake"); err != nil {
		t.Fatal(err)
	}
	if res := f.call(id, "fake__echo", map[string]string{"text": "back"}); res.IsError {
		t.Fatalf("after the restart: %q", resultText(res))
	}
	if err := f.pool.Restart("nothing"); err == nil {
		t.Fatal("restarting an unknown server succeeded")
	}
}

func TestAnIdleInstanceStops(t *testing.T) {
	f := newFixture(t, func(t *tunables) {
		t.idleSession = 50 * time.Millisecond
		t.janitor = 20 * time.Millisecond
	}, stdioServer("fake", nil))
	f.call(agent("s", t.TempDir(), api.RoleDeveloper), "fake__echo", nil)
	eventually(t, "the idle instance to stop", func() bool { return f.state("fake").Instances == 0 })
	if st := f.state("fake"); st.State != api.MCPStateStopped || st.LastError != "" {
		t.Fatalf("an idle stop is not a failure: %+v", st)
	}
}

func TestABusyInstanceIsNotIdle(t *testing.T) {
	f := newFixture(t, func(t *tunables) {
		t.idleSession = 30 * time.Millisecond
		t.janitor = 10 * time.Millisecond
	}, stdioServer("fake", nil))
	res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "fake__sleep", map[string]int{"millis": 300})
	if res.IsError {
		t.Fatalf("a call longer than the idle time was cut: %q", resultText(res))
	}
}

func TestTheProcessLimitIsEnforced(t *testing.T) {
	f := newFixture(t, func(t *tunables) { t.maxProcesses = 2 }, stdioServer("fake", nil))
	for _, s := range []string{"a", "b"} {
		if res := f.call(agent(s, t.TempDir(), api.RoleDeveloper), "fake__echo", nil); res.IsError {
			t.Fatal(resultText(res))
		}
	}
	res := f.call(agent("c", t.TempDir(), api.RoleDeveloper), "fake__echo", nil)
	if !res.IsError || !strings.Contains(resultText(res), "too many MCP server processes") {
		t.Fatalf("a third process answered %q", resultText(res))
	}
	f.pool.EndInstance("a")
	if res := f.call(agent("c", t.TempDir(), api.RoleDeveloper), "fake__echo", nil); res.IsError {
		t.Fatalf("after a session ended: %q", resultText(res))
	}
}

func TestASharedServerRunsOneProcessForEverySession(t *testing.T) {
	s := stdioServer("shared", nil)
	s.Instances = api.MCPInstancesShared
	f := newFixture(t, nil, s)
	a := resultText(f.call(agent("a", t.TempDir(), api.RoleDeveloper), "shared__whoami", nil))
	b := resultText(f.call(agent("b", t.TempDir(), api.RoleDeveloper), "shared__whoami", nil))
	if a != b || !strings.HasSuffix(a, "cwd="+f.home) {
		t.Fatalf("a %q, b %q", a, b)
	}
	cwd := t.TempDir()
	s.Cwd = cwd
	f.set(s)
	f.pool.Changed([]string{"shared"})
	if c := resultText(f.call(agent("a", t.TempDir(), api.RoleDeveloper), "shared__whoami", nil)); !strings.HasSuffix(c, "cwd="+cwd) {
		t.Fatalf("a fixed folder is not used: %q", c)
	}
}

func TestCallTimeoutAndCancellation(t *testing.T) {
	s := stdioServer("fake", nil)
	s.Timeouts.Call = api.Duration(100 * time.Millisecond)
	f := newFixture(t, nil, s)
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	res := f.call(id, "fake__sleep", map[string]int{"millis": 5000})
	if !res.IsError || resultText(res) != "server fake did not answer within 100ms" {
		t.Fatalf("a slow call answered %q", resultText(res))
	}
	s.Timeouts.Call = 0
	f.set(s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Progress proves the upstream is handling the call before cancellation.
	done := make(chan error, 1)
	go func() {
		_, err := f.pool.Call(ctx, id, "fake__sleep", json.RawMessage(`{"millis":60000,"steps":600}`), func(float64, float64, string) { cancel() })
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled call answered %v", err)
		}
	case <-time.After(10 * time.Second):
		// A deadlock guard, unrelated to cancellation latency.
		t.Fatal("a cancelled call did not return")
	}
	if res := f.call(id, "fake__echo", map[string]string{"text": "still"}); res.IsError {
		t.Fatalf("the server is unusable after a cancelled call: %q", resultText(res))
	}
}

func TestProgressIsHandedToTheCaller(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", nil))
	var mu sync.Mutex
	var steps []float64
	// The fake reports each step after its sleep, the last one right before
	// its result. The SDK hands notifications over concurrently, so that
	// last one may arrive after the call has ended, when nobody waits for
	// it any more; the steps reported while the call runs must arrive.
	_, err := f.pool.Call(context.Background(), agent("s", t.TempDir(), api.RoleDeveloper), "fake__sleep", json.RawMessage(`{"millis":300,"steps":3}`), func(done, total float64, msg string) {
		mu.Lock()
		steps = append(steps, done)
		mu.Unlock()
		if total != 3 || msg != "step" {
			t.Errorf("progress %v of %v: %q", done, total, msg)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the progress notifications of the running call", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(steps) >= 2
	})
	mu.Lock()
	defer mu.Unlock()
	if steps[0] != 1 || steps[1] != 2 {
		t.Fatalf("progress %v", steps)
	}
}

func TestUpstreamErrorsAreToolErrors(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", nil))
	res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "fake__rpcerror", map[string]string{"text": "it broke"})
	if !res.IsError || resultText(res) != "server fake: it broke (-32001)" {
		t.Fatalf("answered %q", resultText(res))
	}
}

func TestAServerThatCannotStartIsReported(t *testing.T) {
	s := stdioServer("missing", nil)
	s.Command = filepath.Join(t.TempDir(), "no-such-program")
	f := newFixture(t, nil, s)
	res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "missing__echo", nil)
	if !res.IsError || !strings.Contains(resultText(res), "server missing is not reachable: cannot start") {
		t.Fatalf("answered %q", resultText(res))
	}
	if st := f.state("missing"); !strings.Contains(st.LastError, "cannot start") {
		t.Fatalf("state %+v", st)
	}
}

func TestDisabledServersAreNotOffered(t *testing.T) {
	s := stdioServer("off", nil)
	s.Enabled = false
	f := newFixture(t, nil, s)
	if tools, _ := f.pool.Tools(context.Background(), agent("s", t.TempDir(), api.RoleDeveloper)); len(tools) != 0 {
		t.Fatalf("tools %v", toolNames(tools))
	}
	if st := f.state("off"); st.State != api.MCPStateDisabled {
		t.Fatalf("state %+v", st)
	}
}

func TestAChangedToolListRaisesTheGeneration(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", nil))
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	f.call(id, "fake__echo", nil)
	gen := f.pool.Generation()
	f.call(id, "fake__add_tool", nil)
	eventually(t, "the changed list", func() bool { return f.pool.Generation() > gen })
	tools, _ := f.pool.Tools(context.Background(), id)
	if !slices.Contains(toolNames(tools), "fake__added") {
		t.Fatalf("tools %v", toolNames(tools))
	}
	if res := f.call(id, "fake__added", nil); resultText(res) != "added" {
		t.Fatalf("answered %q", resultText(res))
	}
}

func TestChangedSettingsRestartTheServer(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", nil))
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	before := resultText(f.call(id, "fake__whoami", nil))
	s := stdioServer("fake", nil)
	s.Tools.Deny = []string{"echo"}
	f.set(s)
	gen := f.pool.Generation()
	f.pool.Changed([]string{"fake"})
	if f.pool.Generation() <= gen {
		t.Fatal("a settings change did not raise the generation")
	}
	tools, _ := f.pool.Tools(context.Background(), id)
	if slices.Contains(toolNames(tools), "fake__echo") {
		t.Fatal("a denied tool is offered")
	}
	if after := resultText(f.call(id, "fake__whoami", nil)); pid(after) == pid(before) {
		t.Fatal("the process was not restarted")
	}
	f.set()
	f.pool.Changed([]string{"fake"})
	if st := f.pool.Status(); len(st) != 0 {
		t.Fatalf("a removed server has a state: %+v", st)
	}
}

func TestTestStartsADraftAndKeepsNothing(t *testing.T) {
	f := newFixture(t, nil)
	res, err := f.pool.Test(context.Background(), stdioServer("draft", map[string]string{fakemcp.EnvMode: string(fakemcp.ModeExtras)}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" || !slices.Contains(res.Tools, "draft__echo") || !res.OffersResources || !res.OffersPrompts {
		t.Fatalf("result %+v", res)
	}
	f.pool.mu.Lock()
	n := len(f.pool.instances)
	f.pool.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d instances left", n)
	}
	res, _ = f.pool.Test(context.Background(), stdioServer("draft", map[string]string{fakemcp.EnvMode: string(fakemcp.ModeExit)}))
	if !strings.Contains(res.Error, "exited") {
		t.Fatalf("a failing draft: %+v", res)
	}
}

func TestEndedSessionsLeaveNoProcess(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", nil))
	f.call(agent("s", t.TempDir(), api.RoleDeveloper), "fake__echo", nil)
	f.pool.mu.Lock()
	var proc *process
	for _, inst := range f.pool.instances {
		proc = inst.proc
	}
	f.pool.mu.Unlock()
	f.pool.EndInstance("s")
	select {
	case <-proc.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the process of an ended session still runs")
	}
}
