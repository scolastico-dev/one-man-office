package server

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyGrantState(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var state proxyAuthState
	dest := proxyDestination{ForwardID: "abc", Origin: "https://office.example", URL: "https://office.example/proxy/abc/start?q=1"}
	grant, err := state.issue(dest, now)
	if err != nil || len(grant) < 40 {
		t.Fatalf("issue: %q %v", grant, err)
	}
	if _, _, ok := state.exchange(grant+"x", dest.Origin, now); ok {
		t.Fatal("tampered grant accepted")
	}
	if _, _, ok := state.exchange(grant, "https://other.example", now); ok {
		t.Fatal("wrong destination origin accepted")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	var session string
	for range 20 {
		wg.Go(func() {
			got, token, ok := state.exchange(grant, dest.Origin, now)
			if ok {
				if got != dest {
					t.Error("destination changed")
				}
				session = token
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("exchange winners: %d", wins.Load())
	}
	if !state.authorized(session, now) || state.authorized(session+"x", now) {
		t.Fatal("session validation")
	}
	if state.authorized(session, now.Add(proxySessionTTL)) {
		t.Fatal("expired session accepted")
	}
	grant, _ = state.issue(dest, now)
	if _, _, ok := state.exchange(grant, dest.Origin, now.Add(proxyGrantTTL)); ok {
		t.Fatal("expired grant accepted")
	}
	grant, _ = state.issue(dest, now)
	state.forgetForward(dest.ForwardID)
	if _, _, ok := state.exchange(grant, dest.Origin, now); ok {
		t.Fatal("removed forward grant accepted")
	}
}

func TestProxyStateBoundedAndSwept(t *testing.T) {
	var state proxyAuthState
	now := time.Now()
	dest := proxyDestination{ForwardID: "abc", Origin: "http://localhost", URL: "http://localhost/proxy/abc/"}
	for range proxyAuthLimit {
		if _, err := state.issue(dest, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.issue(dest, now); err == nil {
		t.Fatal("unbounded grants")
	}
	state.sweep(now.Add(proxyGrantTTL))
	if len(state.grants) != 0 {
		t.Fatal("expired grants retained")
	}
	for range proxyAuthLimit {
		grant, err := state.issue(dest, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, ok := state.exchange(grant, dest.Origin, now); !ok {
			t.Fatal("exchange")
		}
	}
	grant, _ := state.issue(dest, now)
	if _, _, ok := state.exchange(grant, dest.Origin, now); ok {
		t.Fatal("unbounded sessions")
	}
	state.sweep(now.Add(proxySessionTTL))
	if len(state.sessions) != 0 {
		t.Fatal("expired sessions retained")
	}
}
