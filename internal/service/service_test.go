package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	return filepath.Join(home, "run")
}

func blockingServe(url string) ServeFunc {
	return func(ctx context.Context, _ io.Writer, ready func(string) error) error {
		if err := ready(url); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}
}

func awaitState(t *testing.T, dir string) runtimeState {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("omo did not publish readiness")
		case <-ticker.C:
			state, err := readState(dir)
			if err == nil && controlRequest(context.Background(), state, "GET", "/status") == nil {
				return state
			}
		}
	}
}

func TestManagedRunAuthenticatesStopAndCleansState(t *testing.T) {
	dir := isolatedHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, nil, "test-launch", blockingServe("http://omo.localhost:1/"))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("omo cleanup timed out")
		}
	})
	state := awaitState(t, dir)
	if state.LaunchID != "test-launch" || strings.Contains(state.URL, "#") {
		t.Fatalf("state: %+v", state)
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{"runtime.json", "omo.log"} {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("private %s: %v %v", name, info, err)
			}
		}
	}
	for _, tc := range []struct{ auth, origin, host string }{
		{}, {auth: "Bearer wrong"}, {auth: "Bearer " + state.Token, origin: "http://localhost"}, {auth: "Bearer " + state.Token, host: "evil.example"},
	} {
		request, _ := http.NewRequest("POST", "http://"+state.Endpoint+"/stop", nil)
		request.Header.Set("Authorization", tc.auth)
		request.Header.Set("Origin", tc.origin)
		if tc.host != "" {
			request.Host = tc.host
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("unauthorized stop: %d", response.StatusCode)
		}
	}
	if err := Run(context.Background(), io.Discard, "", blockingServe("http://omo.localhost:2/")); err == nil {
		t.Fatal("second omo started")
	}
	// The local stop capability remains required even when the browser uses --unsafe.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopCancel()
	if err := Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "runtime.json")); !os.IsNotExist(err) {
		t.Fatalf("runtime not removed: %v", err)
	}
	if err := Stop(stopCtx); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("second stop: %v", err)
	}
}

func TestStopIgnoresStaleRuntimeAndRejectsNonlocalEndpoints(t *testing.T) {
	dir := isolatedHome(t)
	if _, err := prepareDirectory(); err != nil {
		t.Fatal(err)
	}
	contacted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted = true; w.WriteHeader(202) }))
	defer server.Close()
	data, _ := json.Marshal(runtimeState{Endpoint: strings.TrimPrefix(server.URL, "http://"), Token: "stale"})
	if err := writePrivate(filepath.Join(dir, "runtime.json"), data); err != nil {
		t.Fatal(err)
	}
	if err := Stop(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stale stop: %v", err)
	}
	if contacted {
		t.Fatal("stale state contacted an unrelated server")
	}
	for _, endpoint := range []string{"example.com:80", "192.0.2.1:80", "user@127.0.0.1:80", "127.0.0.1:80/other"} {
		if err := controlRequest(context.Background(), runtimeState{Endpoint: endpoint, Token: "secret"}, "POST", "/stop"); err == nil {
			t.Fatalf("accepted %q", endpoint)
		}
	}
}

func TestFailedRunReleasesOwnership(t *testing.T) {
	dir := isolatedHome(t)
	err := Run(context.Background(), nil, "failed", func(context.Context, io.Writer, func(string) error) error {
		return errors.New("invalid listener")
	})
	if err == nil {
		t.Fatal("invalid listener accepted")
	}
	log, readErr := os.ReadFile(filepath.Join(dir, "omo.log"))
	if readErr != nil || !strings.Contains(string(log), "invalid") {
		t.Fatalf("startup diagnostic: %s %v", log, readErr)
	}
	if err := Stop(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("failed launch retained ownership: %v", err)
	}
}

func TestRunReportsRunningURLToSecondCaller(t *testing.T) {
	dir := isolatedHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	const want = "http://omo.localhost:1/#tok"
	go func() { done <- Run(ctx, io.Discard, "report", blockingServe(want)) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("omo cleanup timed out")
		}
	})
	awaitState(t, dir)
	url, running, err := Running(context.Background())
	if err != nil || !running || url != want {
		t.Fatalf("Running = %q %v %v", url, running, err)
	}
	err = Run(context.Background(), io.Discard, "second", blockingServe("http://omo.localhost:3/"))
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second Run: %v", err)
	}
}

func TestRunningIsFalseWhenNothingRuns(t *testing.T) {
	isolatedHome(t)
	url, running, err := Running(context.Background())
	if err != nil || running || url != "" {
		t.Fatalf("Running = %q %v %v", url, running, err)
	}
}

func TestSecondStopHurries(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	serving := make(chan struct{})
	firstStop := make(chan struct{})
	hurried := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	runCtx, runCancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, io.Discard, "", func(ctx context.Context, _ io.Writer, ready func(string) error) error {
			if err := ready("http://x/"); err != nil {
				return err
			}
			close(serving)
			<-ctx.Done()
			close(firstStop)
			select {
			case <-Hurry(ctx):
				close(hurried)
			case <-time.After(10 * time.Second):
			}
			<-release
			return nil
		})
	}()
	t.Cleanup(func() {
		runCancel()
		unblock()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("server did not stop")
		}
	})
	select {
	case <-serving:
	case <-time.After(20 * time.Second):
		t.Fatal("server did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stopped := make(chan error, 2)
	go func() { stopped <- Stop(ctx) }()
	select {
	case <-firstStop:
	case <-time.After(20 * time.Second):
		t.Fatal("server did not receive the first stop")
	}
	select {
	case <-hurried:
		t.Fatal("the first stop hurried")
	default:
	}
	go func() { stopped <- Stop(ctx) }()
	select {
	case <-hurried:
	case <-time.After(5 * time.Second):
		t.Fatal("the second stop did not hurry")
	}
	unblock()
	for i := 0; i < 2; i++ {
		if err := <-stopped; err != nil && !errors.Is(err, ErrNotRunning) {
			t.Fatalf("stop: %v", err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	done <- nil // let cleanup observe the completed server, too
}
