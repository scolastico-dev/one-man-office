package mcprelay

import (
	"context"
	"slices"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/mcpauth"
)

var _ mcpauth.UpstreamStore = (*MemoryStore)(nil)

// MemoryStore is an mcpauth.UpstreamStore that keeps the sign-ins in memory
// only;
// they are lost when omo stops. omo keeps them in its database
// (mcpauth.Upstream); tests use this one.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]mcpauth.UpstreamAuth
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: map[string]mcpauth.UpstreamAuth{}}
}

func (s *MemoryStore) Load(_ context.Context, server string) (mcpauth.UpstreamAuth, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.rows[server]
	return cloneAuth(a), ok, nil
}

func (s *MemoryStore) Save(_ context.Context, a mcpauth.UpstreamAuth) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[a.Server] = cloneAuth(a)
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, server string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, server)
	return nil
}

func cloneAuth(a mcpauth.UpstreamAuth) mcpauth.UpstreamAuth {
	if a.Token != nil {
		t := *a.Token
		a.Token = &t
	}
	a.Registration = slices.Clone(a.Registration)
	a.Scopes = slices.Clone(a.Scopes)
	return a
}
