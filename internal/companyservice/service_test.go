package companyservice

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
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/company"
)

func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	return filepath.Join(home, "company")
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
			t.Fatal("company did not publish readiness")
		case <-ticker.C:
			state, err := readState(dir)
			if err == nil && controlRequest(context.Background(), state, "GET", "/status") == nil {
				return state
			}
		}
	}
}

func TestManagedCompanyAuthenticatesStopAndCleansState(t *testing.T) {
	dir := isolatedHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, company.Options{Listen: "127.0.0.1:0", MaxAgents: 3, Unsafe: true}, nil, "test-launch")
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("company cleanup timed out")
		}
	})
	state := awaitState(t, dir)
	if state.LaunchID != "test-launch" || strings.Contains(state.URL, "#") {
		t.Fatalf("state: %+v", state)
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{"runtime.json", "company.log"} {
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
	if err := Run(context.Background(), company.Options{MaxAgents: 1}, io.Discard, ""); err == nil {
		t.Fatal("second company started")
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
	err := Run(context.Background(), company.Options{Listen: "invalid", MaxAgents: 1}, nil, "failed")
	if err == nil {
		t.Fatal("invalid listener accepted")
	}
	log, readErr := os.ReadFile(filepath.Join(dir, "company.log"))
	if readErr != nil || !strings.Contains(string(log), "invalid") {
		t.Fatalf("startup diagnostic: %s %v", log, readErr)
	}
	if err := Stop(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("failed launch retained ownership: %v", err)
	}
}
