package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
	"gopkg.in/yaml.v3"
)

func registeredClient(t *testing.T, s *Server, endpoint, id string) *Client {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Models = map[string]config.Profile{"shared": {Cmd: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": registeredCredentialRoot}}}
	cfg.Roles = map[string]config.RoleModels{}
	for _, role := range config.AllRoles {
		cfg.Roles[role] = config.RoleModels{Models: []string{"shared"}, Assignment: config.AssignmentRoundRobin}
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".omo", "omo.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	token, err := s.Register(id, dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(endpoint, token)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAuthenticatedAggregateLeasesAndChildExit(t *testing.T) {
	s := New(1, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	a := registeredClient(t, s, h.URL, "a")
	b := registeredClient(t, s, h.URL, "b")
	ctx := context.Background()
	lease, err := a.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Acquire(ctx); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
	if err := b.Release(ctx, lease); err == nil {
		t.Fatal("another child released lease")
	}
	if _, err := b.Acquire(ctx); !errors.Is(err, ErrLimit) {
		t.Fatalf("foreign release freed capacity: %v", err)
	}
	s.Unregister(a.token)
	if err := a.Ping(ctx); err == nil {
		t.Fatal("unregistered token accepted")
	}
	lease, err = b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Release(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(h.URL+"/acquire", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status %d", resp.StatusCode)
	}
}

type usageFetcher struct{ calls atomic.Int32 }

var registeredCredentialRoot = filepath.Join(os.TempDir(), "omo-test-registered-credentials")

type fetchFunc func(context.Context, string, config.Profile) (modelusage.Snapshot, error)

func (f fetchFunc) Fetch(ctx context.Context, key string, p config.Profile) (modelusage.Snapshot, error) {
	return f(ctx, key, p)
}

func (f *usageFetcher) Fetch(_ context.Context, _ string, p config.Profile) (modelusage.Snapshot, error) {
	f.calls.Add(1)
	if p.Env["CLAUDE_CONFIG_DIR"] != registeredCredentialRoot {
		return modelusage.Snapshot{}, errors.New("unregistered credential path")
	}
	return modelusage.Snapshot{UsedPercent: 42, HasSession: true, SessionUsedPercent: 23}, nil
}

func TestUsageUsesRegisteredProfilesAndSharedCache(t *testing.T) {
	f := &usageFetcher{}
	s := New(2, f, time.Minute)
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	a := registeredClient(t, s, h.URL, "a")
	b := registeredClient(t, s, h.URL, "b")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := a
			if i%2 == 1 {
				c = b
			}
			snapshot, err := c.Refresh(context.Background(), "shared", config.Profile{Env: map[string]string{"CLAUDE_CONFIG_DIR": "/attacker"}})
			if err != nil || snapshot.UsedPercent != 42 || snapshot.SessionUsedPercent != 23 {
				t.Errorf("snapshot %#v: %v", snapshot, err)
			}
		}(i)
	}
	wg.Wait()
	if f.calls.Load() != 1 {
		t.Fatalf("upstream calls = %d", f.calls.Load())
	}
	if _, err := a.Fetch(context.Background(), "unknown", config.Profile{}); err == nil {
		t.Fatal("unregistered profile accepted")
	}
	req, _ := http.NewRequest(http.MethodPost, h.URL+"/usage", strings.NewReader(`{"profile":"shared","env":{"CLAUDE_CONFIG_DIR":"/attacker"}}`))
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("arbitrary fields accepted: %d", resp.StatusCode)
	}
}

func TestParentLossFailsClosedAndWatchNotifies(t *testing.T) {
	s := New(1, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	c := registeredClient(t, s, h.URL, "a")
	h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	failure := make(chan error, 1)
	go c.Watch(ctx, func(err error) { failure <- err })
	select {
	case err := <-failure:
		if err == nil {
			t.Fatal("nil failure")
		}
	case <-ctx.Done():
		t.Fatal("watch failed to detect parent death")
	}
	if _, err := c.Acquire(ctx); err == nil {
		t.Fatal("spawn permitted without parent")
	}
	if _, err := c.Fetch(ctx, "shared", config.Profile{}); err == nil {
		t.Fatal("usage permitted without parent")
	}
}

func TestClientEnvironmentAndLoopbackValidation(t *testing.T) {
	t.Setenv("OMO_CONTROL_URL", "")
	t.Setenv("OMO_CONTROL_TOKEN", "")
	if c, err := ClientFromEnv(); c != nil || err != nil {
		t.Fatalf("standalone client = %v %v", c, err)
	}
	t.Setenv("OMO_CONTROL_URL", "http://127.0.0.1:1234")
	if _, err := ClientFromEnv(); err == nil {
		t.Fatal("partial environment silently disabled control")
	}
	if _, err := NewClient("http://example.com:1234", "token"); err == nil {
		t.Fatal("non-loopback endpoint allowed")
	}
}

func TestCancelledWatchDoesNotPoisonLeaseCleanup(t *testing.T) {
	s := New(1, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	c := registeredClient(t, s, h.URL, "one")
	lease, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Watch(ctx, func(error) { t.Fatal("normal shutdown reported parent loss") })
	if err := c.Release(context.Background(), lease); err != nil {
		t.Fatalf("normal watch cancellation prevented lease cleanup: %v", err)
	}
	if used, _ := s.Stats(); used != 0 {
		t.Fatalf("normal shutdown retained %d leases", used)
	}
}

func TestUnregisteredUsageProfilePreventsFurtherSpawning(t *testing.T) {
	s := New(1, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	c := registeredClient(t, s, h.URL, "one")
	if _, err := c.Fetch(context.Background(), "added-after-registration", config.Profile{}); err == nil {
		t.Fatal("unregistered usage profile accepted")
	}
	if _, err := c.Acquire(context.Background()); err == nil {
		t.Fatal("usage authorization failure silently allowed spawn")
	}
}

func TestRegistrationResolvesCredentialRootsAgainstOfficeDirectory(t *testing.T) {
	for _, variable := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR", "HOME"} {
		t.Run(variable, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0755); err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Models = map[string]config.Profile{"relative": {Cmd: "codex", Env: map[string]string{variable: "credentials"}}}
			cfg.Roles = map[string]config.RoleModels{}
			for _, role := range config.AllRoles {
				cfg.Roles[role] = config.RoleModels{Models: []string{"relative"}, Assignment: config.AssignmentRoundRobin}
			}
			raw, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".omo", "omo.yaml"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			s := New(1, fetchFunc(func(_ context.Context, _ string, p config.Profile) (modelusage.Snapshot, error) {
				if p.Env[variable] != filepath.Join(dir, "credentials") {
					return modelusage.Snapshot{}, errors.New("credential root resolved outside child office")
				}
				return modelusage.Snapshot{UsedPercent: 14}, nil
			}), time.Minute)
			token, err := s.Register("child", dir)
			if err != nil {
				t.Fatal(err)
			}
			h := httptest.NewServer(s.Handler())
			defer h.Close()
			c, err := NewClient(h.URL, token)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot, err := c.Fetch(context.Background(), "relative", config.Profile{}); err != nil || snapshot.UsedPercent != 14 {
				t.Fatalf("relative credential fetch: %#v, %v", snapshot, err)
			}
		})
	}
}

func TestConcurrentAcquiresCannotExceedLimit(t *testing.T) {
	s := New(3, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	c := registeredClient(t, s, h.URL, "one")
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Acquire(context.Background()); err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrLimit) {
				t.Errorf("acquire: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := accepted.Load(); got != 3 {
		t.Fatalf("accepted %d concurrent spawns", got)
	}
	s.Unregister(c.token)
	if used, _ := s.Stats(); used != 0 {
		t.Fatalf("child exit left %d leases", used)
	}
}

func TestShellRegistrationOnlyAllowsHeartbeat(t *testing.T) {
	s := New(3, nil, time.Minute)
	token, err := s.RegisterShell("shell")
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	c, err := NewClient(h.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Acquire(context.Background()); err == nil {
		t.Fatal("shell acquired agent capacity")
	}
	if _, err := c.Fetch(context.Background(), "shared", config.Profile{}); err == nil {
		t.Fatal("shell requested usage")
	}
}

type slowBody struct {
	entered chan struct{}
	release chan struct{}
}

func (b *slowBody) Read([]byte) (int, error) { close(b.entered); <-b.release; return 0, io.EOF }
func (b *slowBody) Close() error             { return nil }

func TestSlowChildRequestDoesNotBlockOtherHeartbeats(t *testing.T) {
	s := New(1, nil, time.Minute)
	token, err := s.RegisterShell("shell")
	if err != nil {
		t.Fatal(err)
	}
	b := &slowBody{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(b.release)
	r := httptest.NewRequest(http.MethodPost, "/ping", b)
	r.Header.Set("Authorization", "Bearer "+token)
	go s.Handler().ServeHTTP(httptest.NewRecorder(), r)
	<-b.entered
	ping := httptest.NewRequest(http.MethodPost, "/ping", nil)
	ping.Header.Set("Authorization", "Bearer "+token)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, ping)
		if w.Code != http.StatusOK {
			t.Errorf("ping status %d", w.Code)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("slow request blocked shared control plane")
	}
}
