package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Grants last one minute; sessions last twelve hours without sliding expiry.
// Each store is bounded independently; restarting omo invalidates both.
const (
	proxyGrantTTL   = time.Minute
	proxySessionTTL = 12 * time.Hour
	proxyAuthLimit  = 4096
)

// proxyDestination is built by omo, never supplied by the exchange client.
// Origin binds the grant to the browser origin hosting the exchange endpoint.
type proxyDestination struct {
	ForwardID string
	Origin    string
	URL       string
}

type proxyGrant struct {
	destination proxyDestination
	expires     time.Time
}

type proxyAuthState struct {
	mu       sync.Mutex
	grants   map[string]proxyGrant
	sessions map[string]time.Time
}

func proxySecret() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func (a *proxyAuthState) issue(dest proxyDestination, now time.Time) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked(now)
	if len(a.grants) >= proxyAuthLimit {
		return "", fmt.Errorf("too many pending proxy opens")
	}
	secret, err := proxySecret()
	if err != nil {
		return "", err
	}
	if a.grants == nil {
		a.grants = make(map[string]proxyGrant)
	}
	a.grants[secret] = proxyGrant{dest, now.Add(proxyGrantTTL)}
	return secret, nil
}

// exchange consumes and creates the session atomically, so concurrent exchanges
// have exactly one winner. An incorrect origin cannot consume a valid grant.
func (a *proxyAuthState) exchange(secret, origin string, now time.Time) (proxyDestination, string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked(now)
	grant, ok := a.grants[secret]
	if !ok || grant.destination.Origin != origin {
		return proxyDestination{}, "", false
	}
	delete(a.grants, secret)
	if len(a.sessions) >= proxyAuthLimit {
		return proxyDestination{}, "", false
	}
	token, err := proxySecret()
	if err != nil {
		return proxyDestination{}, "", false
	}
	if a.sessions == nil {
		a.sessions = make(map[string]time.Time)
	}
	a.sessions[token] = now.Add(proxySessionTTL)
	return grant.destination, token, true
}

func (a *proxyAuthState) authorized(token string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	expiry, ok := a.sessions[token]
	if ok && !now.Before(expiry) {
		delete(a.sessions, token)
		return false
	}
	return ok
}

func (a *proxyAuthState) forgetForward(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, grant := range a.grants {
		if grant.destination.ForwardID == id {
			delete(a.grants, key)
		}
	}
}

func (a *proxyAuthState) sweep(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked(now)
}

func (a *proxyAuthState) sweepLocked(now time.Time) {
	for key, grant := range a.grants {
		if !now.Before(grant.expires) {
			delete(a.grants, key)
		}
	}
	for key, expiry := range a.sessions {
		if !now.Before(expiry) {
			delete(a.sessions, key)
		}
	}
}

// Only the Authorization header is considered here. A board token offered in
// WebSocket subprotocols does not turn the app's Authorization into omo's.
func (s *Server) proxyAuthorizationIsOmo(r *http.Request) bool {
	for _, value := range r.Header.Values("Authorization") {
		if s.token != "" && subtle.ConstantTimeCompare([]byte(value), []byte("Bearer "+s.token)) == 1 {
			return true
		}
		candidate := &http.Request{Header: http.Header{"Authorization": {value}}}
		if s.basicAuthorized(candidate) {
			return true
		}
	}
	return false
}

func (a *proxyAuthState) revokeSession(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}
