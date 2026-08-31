package modelusage

import (
	"context"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
)

const DefaultCacheTTL = 10 * time.Minute

type cacheEntry struct {
	snapshot Snapshot
	expires  time.Time
}

type cacheCall struct {
	done     chan struct{}
	snapshot Snapshot
	err      error
}

// Cache stores successful usage responses by credential scope and coalesces
// concurrent misses. Provider errors are returned to current waiters but are
// never retained as cache entries.
type Cache struct {
	Upstream Fetcher
	TTL      time.Duration
	Now      func() time.Time

	mu       sync.Mutex
	entries  map[string]cacheEntry
	inflight map[string]*cacheCall
}

func NewCache(upstream Fetcher, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Cache{Upstream: upstream, TTL: ttl, entries: map[string]cacheEntry{}, inflight: map[string]*cacheCall{}}
}

func (c *Cache) Fetch(ctx context.Context, profileKey string, profile config.Profile) (Snapshot, error) {
	return c.fetch(ctx, profileKey, profile, false)
}

// Refresh bypasses a fresh entry while retaining in-flight coalescing. It is
// used by the scheduler so the next agent spawn can remain cache-only.
func (c *Cache) Refresh(ctx context.Context, profileKey string, profile config.Profile) (Snapshot, error) {
	return c.fetch(ctx, profileKey, profile, true)
}

func (c *Cache) fetch(ctx context.Context, profileKey string, profile config.Profile, force bool) (Snapshot, error) {
	key := Scope(profile)
	if key == "" {
		key = profileKey
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]cacheEntry{}
	}
	if c.inflight == nil {
		c.inflight = map[string]*cacheCall{}
	}
	if !force {
		if entry, ok := c.entries[key]; ok && now().Before(entry.expires) {
			c.mu.Unlock()
			return entry.snapshot, nil
		}
	}
	if call := c.inflight[key]; call != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		case <-call.done:
			return call.snapshot, call.err
		}
	}
	call := &cacheCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	call.snapshot, call.err = c.Upstream.Fetch(ctx, profileKey, profile)
	c.mu.Lock()
	if call.err == nil {
		c.entries[key] = cacheEntry{snapshot: call.snapshot, expires: now().Add(c.TTL)}
	}
	delete(c.inflight, key)
	close(call.done)
	c.mu.Unlock()
	return call.snapshot, call.err
}
