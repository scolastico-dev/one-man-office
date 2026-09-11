package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

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

func TestPingPublishesExactBoundedLiveState(t *testing.T) {
	s := New(2, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Models = map[string]config.Profile{"shared": {Cmd: "claude"}}
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
	token, err := s.Register("office", dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(h.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	c.SetSnapshotProvider(func() (LiveState, error) {
		return LiveState{
			Agents:  []AgentState{{Name: "é" + strings.Repeat("a", 300), Role: "developer", State: "working", JobID: 7, Step: "ship"}},
			TUI:     TUIState{Mode: "peek", Peek: "developer-ada"},
			Actions: []ActionState{{Plugin: "tools", Action: "run", Description: "Run it", Args: true}},
		}, nil
	})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := s.Snapshot("office")
	if len(got.Agents) != 1 || got.Agents[0].JobID != 7 || got.Agents[0].Role != "developer" {
		t.Fatalf("agents = %#v", got.Agents)
	}
	if len(got.Agents[0].Name) > 256 || !utf8.ValidString(got.Agents[0].Name) {
		t.Fatalf("agent name is not bounded UTF-8: %q", got.Agents[0].Name)
	}
	want := LiveState{
		Agents:  []AgentState{{Name: got.Agents[0].Name, Role: "developer", State: "working", JobID: 7, Step: "ship"}},
		TUI:     TUIState{Mode: "peek", Peek: "developer-ada"},
		Actions: []ActionState{{Plugin: "tools", Action: "run", Description: "Run it", Args: true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("live state = %#v, want %#v", got, want)
	}
	rawState, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawState), "\"agents\":[") ||
		!strings.Contains(string(rawState), "\"tui\":{\"mode\":\"peek\",\"peek\":\"developer-ada\"}") ||
		!strings.Contains(string(rawState), "\"actions\":[{\"plugin\":\"tools\",\"action\":\"run\",\"description\":\"Run it\",\"args\":true}]") {
		t.Fatalf("live state JSON = %s", rawState)
	}
}

func TestPingWireBodyHasExactLiveStateJSON(t *testing.T) {
	wire := make(chan []byte, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		wire <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	defer h.Close()
	c, err := NewClient(h.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	c.SetSnapshotProvider(func() (LiveState, error) {
		return LiveState{
			Agents:  []AgentState{{Name: "agent", Role: "developer", State: "working", JobID: 7, Step: "testing"}},
			TUI:     TUIState{Mode: "peek", Peek: "developer-ada"},
			Actions: []ActionState{{Plugin: "tools", Action: "run", Description: "Run it", Args: true}},
		}, nil
	})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-wire:
		want := `{"agents":[{"name":"agent","role":"developer","state":"working","job_id":7,"step":"testing"}],"tui":{"mode":"peek","peek":"developer-ada"},"actions":[{"plugin":"tools","action":"run","description":"Run it","args":true}]}`
		if string(body) != want {
			t.Fatalf("wire body = %s, want %s", body, want)
		}
	case <-time.After(time.Second):
		t.Fatal("did not capture ping body")
	}
}

func TestPingAcceptsLiveStateAboveFormerBodyCap(t *testing.T) {
	s := New(1, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	token, err := s.RegisterShell("shell")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"agents":[],"tui":{"mode":"` + strings.Repeat("x", 5<<20) + `","peek":""},"actions":[]}`
	req, err := http.NewRequest(http.MethodPost, h.URL+"/ping", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("oversize ping rejected: HTTP %d", resp.StatusCode)
	}
}

func TestLiveStateSnapshotIsDefensivelyCopied(t *testing.T) {
	s := New(1, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	token, err := s.RegisterShell("shell")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(h.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	c.SetSnapshotProvider(func() (LiveState, error) {
		return LiveState{Agents: []AgentState{{Name: "shell", Role: "shell", State: "working"}}}, nil
	})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := s.Snapshot("shell")
	first.Agents[0].Name = "mutated"
	second := s.Snapshot("shell")
	if second.Agents[0].Name != "shell" {
		t.Fatalf("stored snapshot aliased returned value: %#v", second)
	}
}

func TestPingRejectsUnknownAndMalformedLiveStateFields(t *testing.T) {
	s := New(1, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	token, err := s.RegisterShell("shell")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"agents":[],"tui":{},"actions":[],"extra":true}`,
		`{"agents":"not-an-array","tui":{},"actions":[]}`,
		`{"agents":[],"tui":{"mode":7},"actions":[]}`,
		`{"agents":null,"tui":{},"actions":[]}`,
	} {
		req, err := http.NewRequest(http.MethodPost, h.URL+"/ping", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s accepted with HTTP %d", body, resp.StatusCode)
		}
	}
}

func TestPingTruncatesOversizeListsAndMultibyteStrings(t *testing.T) {
	s := New(1, nil, time.Minute)
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	token, err := s.RegisterShell("shell")
	if err != nil {
		t.Fatal(err)
	}
	tooLong := strings.Repeat("界", 200)
	payload := pingRequest{TUI: TUIState{Mode: tooLong, Peek: tooLong}}
	for range 300 {
		payload.Agents = append(payload.Agents, AgentState{Name: tooLong, Role: tooLong, State: tooLong, Step: tooLong})
	}
	for range 200 {
		payload.Actions = append(payload.Actions, ActionState{Plugin: tooLong, Action: tooLong, Description: tooLong})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, h.URL+"/ping", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("oversize live state rejected: HTTP %d", resp.StatusCode)
	}
	state := s.Snapshot("shell")
	if len(state.Agents) != maxLiveAgents || len(state.Actions) != maxLiveActions {
		t.Fatalf("bounded list lengths = %d/%d", len(state.Agents), len(state.Actions))
	}
	if len(state.Agents[0].Name) > maxLiveStringBytes || !utf8.ValidString(state.Agents[0].Name) ||
		len(state.TUI.Mode) > maxLiveStringBytes || !utf8.ValidString(state.TUI.Mode) {
		t.Fatalf("multibyte values were not safely truncated")
	}
}

func TestWatchSendsPromptHeartbeatWithBurstCoalescing(t *testing.T) {
	s := New(1, nil, time.Minute)
	var pings atomic.Int32
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pings.Add(1)
		s.Handler().ServeHTTP(w, r)
	}))
	defer h.Close()
	token, err := s.RegisterShell("shell")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(h.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		c.Watch(ctx, func(error) { t.Error("watch reported failure") })
		close(done)
	}()
	for {
		if pings.Load() >= 1 {
			break
		}
		select {
		case <-done:
			t.Fatal("watch stopped before first heartbeat")
		case <-time.After(10 * time.Millisecond):
		}
	}
	for range 20 {
		c.NotifyHeartbeat()
	}
	for {
		if pings.Load() >= 2 {
			break
		}
		select {
		case <-done:
			t.Fatal("watch stopped after notification")
		case <-time.After(10 * time.Millisecond):
		}
	}
	count := pings.Load()
	for range 20 {
		c.NotifyHeartbeat()
	}
	select {
	case <-time.After(100 * time.Millisecond):
		if pings.Load() != count {
			t.Fatalf("burst sent %d additional heartbeats in debounce window", pings.Load()-count)
		}
	case <-done:
		t.Fatal("watch stopped during burst")
	}
	cancel()
	<-done
}

func TestSnapshotProviderErrorDoesNotStopWatch(t *testing.T) {
	s := New(1, nil, time.Minute)
	var pings atomic.Int32
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pings.Add(1)
		s.Handler().ServeHTTP(w, r)
	}))
	defer h.Close()
	token, err := s.RegisterShell("shell")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(h.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	c.SetSnapshotProvider(func() (LiveState, error) {
		return LiveState{}, errors.New("snapshot unavailable")
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	failure := make(chan error, 1)
	go func() {
		c.Watch(ctx, func(err error) { failure <- err })
		close(done)
	}()
	deadline := time.After(2 * time.Second)
	for pings.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("snapshot error stopped ordinary heartbeat")
		case err := <-failure:
			t.Fatalf("snapshot error became parent failure: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
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
