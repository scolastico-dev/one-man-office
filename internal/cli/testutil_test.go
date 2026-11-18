package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"github.com/scolastico-dev/one-man-office/internal/service"
	"github.com/scolastico-dev/one-man-office/internal/testport"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// isolate points the omo home and the login autostart location into the
// test's temporary directories and removes inherited omo state.
func isolate(t *testing.T) string {
	t.Helper()
	childenvtest.ClearOmo(t)
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return home
}

// execute runs one omo invocation to completion.
func execute(ctx context.Context, args ...string) (string, error) {
	cmd := Root("test")
	var out syncBuffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	return out.String(), err
}

func mustExecute(t *testing.T, args ...string) string {
	t.Helper()
	out, err := execute(context.Background(), args...)
	if err != nil {
		t.Fatalf("omo %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

type running struct {
	out  *syncBuffer
	done chan error
	stop context.CancelFunc
}

var fragment = regexp.MustCompile(`#([0-9a-f]{64})`)

// startRoot runs the foreground server in a goroutine on a free port and
// waits until it answers its control endpoint.
func startRoot(t *testing.T) *running {
	t.Helper()
	var r *running
	_, err := testport.Acquire(func(port int) error {
		var err error
		r, err = startRootOnPort(t, port)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.shutdown(t) })
	return r
}

func startRootOnPort(t *testing.T, port int) (*running, error) {
	t.Helper()
	if out, err := execute(context.Background(), "config", "set", "port", strconv.Itoa(port)); err != nil {
		return nil, fmt.Errorf("configure fixture port: %w\n%s", err, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := Root("test")
	r := &running{out: &syncBuffer{}, done: make(chan error, 1), stop: cancel}
	cmd.SetOut(r.out)
	cmd.SetErr(r.out)
	cmd.SetArgs(nil)
	go func() { r.done <- cmd.ExecuteContext(ctx) }()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-r.done:
			cancel() // ExecuteContext has returned and released its server.
			return nil, fmt.Errorf("server exited early: %w\n%s", err, r.out.String())
		default:
		}
		if _, ok, _ := service.Running(context.Background()); ok {
			return r, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	r.shutdown(t)
	return nil, fmt.Errorf("server did not become ready\n%s", r.out.String())
}

func (r *running) shutdown(t *testing.T) {
	r.stop()
	select {
	case <-r.done:
		r.done <- nil
	case <-time.After(15 * time.Second):
		t.Error("server did not stop")
	}
}

func tokenFile(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(os.Getenv("OMO_HOME"), "token"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func TestIsolateDropsInheritedOmo(t *testing.T) {
	childenvtest.SetOmo(t)
	home := isolate(t)
	childenvtest.CheckOmo(t, os.Environ(), map[string]string{"OMO_HOME": home})
}
