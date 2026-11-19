//go:build !windows

package userterm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
)

func script(s string) func() (string, []string) {
	return func() (string, []string) { return "/bin/sh", []string{"-c", s} }
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func readTrim(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) inc() { c.mu.Lock(); c.n++; c.mu.Unlock() }
func (c *counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func TestOpenRunsShellInDir(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "pwd")
	m := New(Options{Shell: script(fmt.Sprintf("pwd > %q", out))})
	defer m.CloseAll(time.Second)
	info, err := m.Open(Spec{Dir: dir, Title: "t", Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "running" || info.Dir != dir || len(info.ID) != 32 {
		t.Fatalf("info %+v", info)
	}
	waitFor(t, "pwd", func() bool { return readTrim(out) != "" })
	if got := readTrim(out); got != dir {
		t.Fatalf("pwd = %q, want %q", got, dir)
	}
	if _, ok := m.Terminal(info.ID); !ok {
		t.Fatal("terminal missing")
	}
}

func TestShellRunsWithEnvironmentWithoutIdentity(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env")
	m := New(Options{
		Shell: script(fmt.Sprintf("env > %q", out)),
		Env: func() []string {
			env := childenvtest.ShellEnv([]string{"PATH=/usr/bin:/bin"}, t.TempDir())
			env = append(env, "OMO_AGENT_ID=zzz")
			return append(ShellEnv(env, "/s.sock"), "OMO_AGENT_ID=zzz")
		},
	})
	defer m.CloseAll(time.Second)
	if _, err := m.Open(Spec{Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "env", func() bool { return strings.Contains(readTrim(out), "OMO_SOCKET") })
	got := readTrim(out)
	if strings.Contains(got, "OMO_AGENT_ID") || strings.Contains(got, "zzz") || !strings.Contains(got, "OMO_SOCKET=/s.sock") {
		t.Fatalf("environment:\n%s", got)
	}
}

func TestHoldReleasedOnceWhenShellExits(t *testing.T) {
	var held, released counter
	dir := t.TempDir()
	m := New(Options{
		Shell: script("exit 0"),
		Hold: func(d string) func() {
			if d != dir {
				t.Errorf("hold on %q", d)
			}
			held.inc()
			return released.inc
		},
	})
	defer m.CloseAll(time.Second)
	info, err := m.Open(Spec{Dir: dir, Hold: true})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "release", func() bool { return released.get() == 1 })
	waitFor(t, "exited state", func() bool { return m.List()[0].State == "exited" })
	if err := m.Close(info.ID); err != nil {
		t.Fatal(err)
	}
	m.CloseAll(time.Second)
	if held.get() != 1 || released.get() != 1 {
		t.Fatalf("held %d released %d", held.get(), released.get())
	}
}

func TestHoldOnlyWhenAsked(t *testing.T) {
	var held counter
	m := New(Options{Shell: script("exit 0"), Hold: func(string) func() { held.inc(); return func() {} }})
	defer m.CloseAll(time.Second)
	if _, err := m.Open(Spec{Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if held.get() != 0 {
		t.Fatal("hold taken without Hold")
	}
}

func TestStartFailureReleasesHold(t *testing.T) {
	var held, released counter
	m := New(Options{
		Shell: func() (string, []string) { return "/nonexistent/shell", nil },
		Hold:  func(string) func() { held.inc(); return released.inc },
	})
	if _, err := m.Open(Spec{Dir: t.TempDir(), Hold: true}); err == nil {
		t.Fatal("expected error")
	}
	if held.get() != 1 || released.get() != 1 || len(m.List()) != 0 {
		t.Fatalf("held %d released %d list %v", held.get(), released.get(), m.List())
	}
	// The failed start must not count against the limit.
	m2 := New(Options{MaxRunning: 1, Shell: func() (string, []string) { return "/nonexistent/shell", nil }})
	for range 3 {
		if _, err := m2.Open(Spec{Dir: t.TempDir()}); errors.Is(err, ErrTooMany) {
			t.Fatal("failed starts count as running")
		}
	}
}

func TestVerifyRefusesStartAndReleasesHold(t *testing.T) {
	var held, released, verified counter
	// An invalid executable makes a start attempted before verification fail
	// synchronously, rather than relying on a shell creating a marker in time.
	m := New(Options{
		Shell: func() (string, []string) { return "/nonexistent/shell", nil },
		Hold:  func(string) func() { held.inc(); return released.inc },
	})
	refused := errors.New("the folder changed")
	_, err := m.Open(Spec{Dir: t.TempDir(), Hold: true, Verify: func() error { verified.inc(); return refused }})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the verify error", err)
	}
	if verified.get() != 1 || held.get() != 1 || released.get() != 1 || len(m.List()) != 0 {
		t.Fatalf("verified %d held %d released %d list %v", verified.get(), held.get(), released.get(), m.List())
	}
}

func TestCloseEndsRunningShellAndReleases(t *testing.T) {
	var released counter
	pidFile := filepath.Join(t.TempDir(), "pids")
	// The shell starts a background child in its own process group and waits.
	m := New(Options{
		Shell: script(fmt.Sprintf("echo $$ > %q; sleep 300 & echo $! >> %q; wait", pidFile, pidFile)),
		Hold:  func(string) func() { return released.inc },
	})
	defer m.CloseAll(time.Second)
	info, err := m.Open(Spec{Dir: t.TempDir(), Hold: true})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "pids", func() bool { return len(strings.Fields(readTrim(pidFile))) == 2 })
	var pids []int
	for _, f := range strings.Fields(readTrim(pidFile)) {
		p, _ := strconv.Atoi(f)
		pids = append(pids, p)
	}
	if err := m.Close(info.ID); err != nil {
		t.Fatal(err)
	}
	if released.get() != 1 {
		t.Fatalf("released %d", released.get())
	}
	if len(m.List()) != 0 {
		t.Fatal("closed shell still listed")
	}
	if _, ok := m.Terminal(info.ID); ok {
		t.Fatal("closed shell still has a terminal")
	}
	if !errors.Is(m.Close(info.ID), ErrUnknown) {
		t.Fatal("second Close should be ErrUnknown")
	}
	waitFor(t, "processes gone", func() bool {
		for _, p := range pids {
			if alive(p) {
				// A zombie awaiting reaping still answers signal 0; reaped by Wait normally.
				return false
			}
		}
		return true
	})
}

func TestCloseAllReleasesEveryHoldAndRefusesOpen(t *testing.T) {
	var held, released counter
	m := New(Options{
		Shell: script("sleep 300"),
		Hold:  func(string) func() { held.inc(); return released.inc },
	})
	defer m.CloseAll(time.Second)
	var ids []string
	for range 3 {
		info, err := m.Open(Spec{Dir: t.TempDir(), Hold: true})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, info.ID)
	}
	terms := map[string]interface{ Done() <-chan struct{} }{}
	for _, id := range ids {
		tm, _ := m.Terminal(id)
		terms[id] = tm
	}
	m.CloseAll(5 * time.Second)
	if released.get() != 3 || held.get() != 3 {
		t.Fatalf("held %d released %d", held.get(), released.get())
	}
	for id, tm := range terms {
		select {
		case <-tm.Done():
		default:
			t.Errorf("shell %s still running", id)
		}
	}
	if _, err := m.Open(Spec{Dir: t.TempDir()}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Open after CloseAll: %v", err)
	}
	m.CloseAll(time.Second)
}

func TestMaxRunningAndKept(t *testing.T) {
	var mu sync.Mutex
	var changes []Change
	m := New(Options{
		MaxRunning: 2, MaxKept: 2,
		Shell:    script("sleep 300"),
		OnChange: func(c Change) { mu.Lock(); changes = append(changes, c); mu.Unlock() },
	})
	defer m.CloseAll(time.Second)
	a, err := m.Open(Spec{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(Spec{Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(Spec{Dir: t.TempDir()}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("third open: %v", err)
	}
	if err := m.Close(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(Spec{Dir: t.TempDir()}); err != nil {
		t.Fatalf("slot not freed: %v", err)
	}

	// Exited shells do not count as running and are kept up to MaxKept.
	k := New(Options{
		MaxRunning: 1, MaxKept: 2, Shell: script("exit 0"),
		OnChange: func(c Change) { mu.Lock(); changes = append(changes, c); mu.Unlock() },
	})
	defer k.CloseAll(time.Second)
	mu.Lock()
	changes = nil
	mu.Unlock()
	var ids []string
	for i := 0; i < 4; i++ {
		var info Info
		waitFor(t, "free slot", func() bool {
			var err error
			info, err = k.Open(Spec{Dir: t.TempDir()})
			return err == nil
		})
		ids = append(ids, info.ID)
		id := info.ID
		waitFor(t, "exit", func() bool {
			for _, i := range k.List() {
				if i.ID == id {
					return i.State == "exited"
				}
			}
			return true
		})
	}
	list := k.List()
	if len(list) != 2 || list[0].ID != ids[2] || list[1].ID != ids[3] {
		t.Fatalf("kept %v, want the newest two of %v", list, ids)
	}
	mu.Lock()
	defer mu.Unlock()
	var closed []string
	for _, c := range changes {
		if c.Kind == "closed" {
			closed = append(closed, c.Info.ID)
		}
	}
	if len(closed) != 2 || closed[0] != ids[0] || closed[1] != ids[1] {
		t.Fatalf("closed %v, want oldest first %v", closed, ids[:2])
	}
}

func TestChangesInOrder(t *testing.T) {
	var mu sync.Mutex
	var kinds []string
	var m *Manager
	m = New(Options{
		Shell: script("exit 3"),
		OnChange: func(c Change) {
			_ = m.List() // never called under the manager's lock
			mu.Lock()
			kinds = append(kinds, c.Kind)
			mu.Unlock()
		},
	})
	defer m.CloseAll(time.Second)
	info, err := m.Open(Spec{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "exited", func() bool { mu.Lock(); defer mu.Unlock(); return len(kinds) == 2 })
	l := m.List()
	if len(l) != 1 || l[0].State != "exited" || l[0].ExitError == "" || l[0].EndedAt == nil {
		t.Fatalf("list %+v", l)
	}
	if err := m.Close(info.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(kinds, ",") != "opened,exited,closed" {
		t.Fatalf("kinds %v", kinds)
	}
}

func TestConcurrentOpenCloseList(t *testing.T) {
	m := New(Options{MaxRunning: 4, Shell: script("sleep 300"), Hold: func(string) func() { return func() {} }})
	defer m.CloseAll(time.Second)
	dir := t.TempDir()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				info, err := m.Open(Spec{Dir: dir, Hold: true})
				_ = m.List()
				if err == nil {
					_ = m.Close(info.ID)
				}
			}
		}()
	}
	wg.Wait()
	m.CloseAll(2 * time.Second)
	if len(m.List()) != 0 {
		t.Fatal("shells left")
	}
}

func TestDefaultShellUsesAbsoluteExecutableSHELL(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	if cmd, _ := DefaultShell(); cmd != "/bin/sh" {
		t.Fatalf("got %q", cmd)
	}
	exe := filepath.Join(t.TempDir(), "myshell")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", exe)
	if cmd, _ := DefaultShell(); cmd != exe {
		t.Fatalf("got %q, want %q", cmd, exe)
	}
	notExec := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(notExec, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"", "bash", "./sh", notExec, filepath.Join(t.TempDir(), "missing")} {
		t.Setenv("SHELL", v)
		if cmd, _ := DefaultShell(); cmd != "/bin/sh" {
			t.Errorf("SHELL=%q gave %q", v, cmd)
		}
	}
}
