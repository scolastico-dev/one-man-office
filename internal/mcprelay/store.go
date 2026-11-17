package mcprelay

import (
	"context"
	"encoding/json"
	"slices"
	"sync"

	"golang.org/x/oauth2"
)

// Values of UpstreamAuth.State.
const (
	AuthStateConnected  = "connected"
	AuthStateNeedsLogin = "needs_login"
	AuthStateError      = "error"
)

// UpstreamAuth is omo's sign-in at one upstream server with auth oauth: the
// OAuth client omo used (pre-registered or registered by omo), where its
// authorization server is, and the current tokens. Tokens are credentials:
// they are never logged, emitted or answered.
type UpstreamAuth struct {
	Server       string
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURI is the callback address the client was registered with;
	// a registration made for another address is not reused.
	RedirectURI string
	// State is AuthStateConnected, AuthStateNeedsLogin or AuthStateError;
	// Error is the cleaned reason of AuthStateError.
	State string
	Error string
	// Registration holds what omo needs to refresh the token after a
	// restart: the endpoints and how the client authenticates.
	Registration json.RawMessage
	Token        *oauth2.Token
	Scopes       []string
}

// UpstreamStore keeps omo's sign-ins at upstream servers, one per server.
type UpstreamStore interface {
	Load(ctx context.Context, server string) (UpstreamAuth, bool, error)
	Save(ctx context.Context, a UpstreamAuth) error
	Delete(ctx context.Context, server string) error
}

// MemoryStore is an UpstreamStore that keeps the sign-ins in memory only;
// they are lost when omo stops.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]UpstreamAuth
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: map[string]UpstreamAuth{}}
}

func (s *MemoryStore) Load(_ context.Context, server string) (UpstreamAuth, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.rows[server]
	return cloneAuth(a), ok, nil
}

func (s *MemoryStore) Save(_ context.Context, a UpstreamAuth) error {
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

func cloneAuth(a UpstreamAuth) UpstreamAuth {
	if a.Token != nil {
		t := *a.Token
		a.Token = &t
	}
	a.Registration = slices.Clone(a.Registration)
	a.Scopes = slices.Clone(a.Scopes)
	return a
}
