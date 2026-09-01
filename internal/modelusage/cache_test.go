package modelusage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
)

type blockingUsageFetcher struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func (f *blockingUsageFetcher) Fetch(context.Context, string, config.Profile) (Snapshot, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	<-f.release
	return Snapshot{UsedPercent: 42}, nil
}

func (f *blockingUsageFetcher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestCacheCoalescesConcurrentCredentialRequests(t *testing.T) {
	upstream := &blockingUsageFetcher{release: make(chan struct{})}
	cache := NewCache(upstream, time.Minute)
	profile := config.Profile{Cmd: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/shared-cache-test"}}
	const callers = 5
	results := make(chan Snapshot, callers)
	for range callers {
		go func() {
			snapshot, err := cache.Fetch(context.Background(), "claude-model", profile)
			if err != nil {
				t.Errorf("fetch: %v", err)
			}
			results <- snapshot
		}()
	}
	deadline := time.Now().Add(time.Second)
	for upstream.count() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := upstream.count(); got != 1 {
		t.Fatalf("concurrent upstream calls = %d, want 1", got)
	}
	close(upstream.release)
	for range callers {
		if got := <-results; got.UsedPercent != 42 {
			t.Fatalf("snapshot = %+v", got)
		}
	}
	if _, err := cache.Fetch(context.Background(), "another-model", profile); err != nil {
		t.Fatal(err)
	}
	if got := upstream.count(); got != 1 {
		t.Fatalf("fresh cache made %d upstream calls", got)
	}
}

func TestCacheRefreshBypassesFreshEntry(t *testing.T) {
	upstream := &blockingUsageFetcher{release: make(chan struct{})}
	close(upstream.release)
	cache := NewCache(upstream, time.Hour)
	profile := config.Profile{Cmd: "codex", Env: map[string]string{"CODEX_HOME": "/tmp/codex-cache-test"}}
	if _, err := cache.Fetch(context.Background(), "first", profile); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Refresh(context.Background(), "second", profile); err != nil {
		t.Fatal(err)
	}
	if got := upstream.count(); got != 2 {
		t.Fatalf("refresh calls = %d, want 2", got)
	}
}
