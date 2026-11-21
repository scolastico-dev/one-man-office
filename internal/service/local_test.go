package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/filelock"
)

// The detached child is this test executable, with the same launch argument
// as the real CLI. Its optional socket gate makes concurrent starts exact.
func detachedFixture() bool {
	if len(os.Args) != 2 || !strings.HasPrefix(os.Args[1], "--background-id=") {
		return false
	}
	id := strings.TrimPrefix(os.Args[1], "--background-id=")
	if gate := os.Getenv("TEST_LOCAL_LAUNCH_GATE"); gate != "" {
		conn, err := net.Dial("tcp", gate)
		if err != nil {
			panic(err)
		}
		if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
			panic(err)
		}
		conn.Close()
	}
	if err := Run(context.Background(), nil, id, blockingServe("http://localhost:8090/#fixture")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return true
}

func startFixture(t *testing.T, id string) runtimeState {
	t.Helper()
	dir, err := directory()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, io.Discard, id, blockingServe("http://localhost:8090/#external")) }()
	t.Cleanup(func() { cancel(); <-done })
	return awaitState(t, dir)
}

func TestLocalStartAttachesWithoutExecutingMissingBinary(t *testing.T) {
	isolatedHome(t)
	want := startFixture(t, "external")
	got, err := StartOrAttach(context.Background(), filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Owned || got.Token != want.Token || got.Endpoint != want.Endpoint || got.URL != want.URL {
		t.Fatalf("attach = %+v", got)
	}
}

func TestLocalStartOwnsOnlyItsNewDetachedInstance(t *testing.T) {
	isolatedHome(t)
	got, err := StartOrAttach(context.Background(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Stop(context.Background()) })
	if !got.Owned || got.Token == "" || got.LaunchID == "" || got.URL != "http://localhost:8090/#fixture" {
		t.Fatalf("start = %+v", got)
	}
	attached, err := StartOrAttach(context.Background(), "must-not-execute")
	if err != nil || attached.Owned || attached.Token != got.Token {
		t.Fatalf("second start = %+v, %v", attached, err)
	}
}

func TestLocalStartMissingExecutableDoesNotReportOwnership(t *testing.T) {
	isolatedHome(t)
	got, err := StartOrAttach(context.Background(), filepath.Join(t.TempDir(), "absent"))
	if err == nil || got.Owned || got.Token != "" {
		t.Fatalf("missing executable = %+v, %v", got, err)
	}
}

func TestLocalStartWaitsForConcurrentLaunchThenAttaches(t *testing.T) {
	dir := isolatedHome(t)
	if _, err := prepareDirectory(); err != nil {
		t.Fatal(err)
	}
	// A normal detached launch holds launch.lock until it publishes readiness.
	launch, err := filelock.Acquire(context.Background(), filepath.Join(dir, "launch.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer launch.Close()
	result := make(chan Instance, 1)
	failure := make(chan error, 1)
	go func() {
		value, err := StartOrAttach(context.Background(), "must-not-execute")
		result <- value
		failure <- err
	}()
	want := startFixture(t, "concurrent-normal-launch")
	if err := launch.Close(); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if err := <-failure; err != nil || got.Owned || got.Token != want.Token {
		t.Fatalf("concurrent attach = %+v, %v", got, err)
	}
}

func TestLocalStartLosesRunLockRaceWithoutClaimingOtherInstance(t *testing.T) {
	isolatedHome(t)
	gate, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	t.Setenv("TEST_LOCAL_LAUNCH_GATE", gate.Addr().String())
	result := make(chan Instance, 1)
	failure := make(chan error, 1)
	go func() {
		value, err := StartOrAttach(context.Background(), os.Args[0])
		result <- value
		failure <- err
	}()
	child, err := gate.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	want := startFixture(t, "external-race-winner")
	_, _ = child.Write([]byte{1})
	got := <-result
	if err := <-failure; err != nil || got.Owned || got.Token != want.Token {
		t.Fatalf("lost race = %+v, %v", got, err)
	}
}

func TestCapturedControlCannotStopReplacement(t *testing.T) {
	dir := isolatedHome(t)
	old := startFixture(t, "same-launch-id")
	if err := Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	replacement := startFixture(t, "same-launch-id")
	if old.Token == replacement.Token {
		t.Fatal("replacement reused control capability")
	}
	// Simulate endpoint reuse as well as matching launch IDs. Only the
	// per-run capability distinguishes the replacement at the final request.
	old.Endpoint = replacement.Endpoint
	if err := controlRequest(context.Background(), old, "POST", "/stop"); err == nil {
		t.Fatal("old capability stopped replacement")
	}
	if err := controlRequest(context.Background(), replacement, "GET", "/status"); err != nil {
		t.Fatal(err)
	}
	state, err := readState(dir)
	if err != nil || state.Token != replacement.Token {
		t.Fatalf("replacement state changed: %v", err)
	}
}

func TestLocalStartCanceledWhileAnotherLaunchOwnsLock(t *testing.T) {
	dir := isolatedHome(t)
	if _, err := prepareDirectory(); err != nil {
		t.Fatal(err)
	}
	lock, err := filelock.Acquire(context.Background(), filepath.Join(dir, "launch.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := StartOrAttach(ctx, "must-not-execute"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled launch = %v", err)
	}
}
