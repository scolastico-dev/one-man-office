package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// An answer to a plugin operation is sent only after the operation's claim
// on the plugin is released: the next request for the same plugin, started
// when the answer arrives, is never refused as "being installed".
func TestPluginOpAnswersAfterReleasingTheClaim(t *testing.T) {
	s := newTestServer(t)
	for i := range 30000 {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/plugins", nil)
		s.pluginOp(w, r, "same", http.StatusOK, time.Minute, func(context.Context) (any, error) { return nil, nil })
		release, err := s.claimPlugin("same")
		if err != nil {
			t.Fatalf("round %d: the answer came while the plugin was still claimed: %v", i, err)
		}
		release()
	}
}

// A claim released twice (the work's end and a refused start) frees the
// plugin once: the second call does not free a later claim.
func TestPluginClaimReleaseIsOnce(t *testing.T) {
	s := newTestServer(t)
	first, err := s.claimPlugin("p")
	if err != nil {
		t.Fatal(err)
	}
	first()
	second, err := s.claimPlugin("p")
	if err != nil {
		t.Fatal(err)
	}
	first()
	if _, err := s.claimPlugin("p"); err == nil {
		t.Fatal("a second release of the first claim freed the second claim")
	}
	second()
}

// The same holds for the usage script test and the environment check: when
// their answer has arrived, the lock that allows only one at a time is free.
func TestOneAtATimeLocksAreFreeWhenTheAnswerArrives(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	for i := range 3000 {
		resp := asUser(b, s, "POST", "/api/usage/scripts/probe/test", api.UsageTest{Text: emptyScript})
		wantStatus(t, resp.StatusCode, http.StatusOK)
		resp.Body.Close()
		if !s.usage.test.TryLock() {
			t.Fatalf("round %d: the usage test answered while its lock was held", i)
		}
		s.usage.test.Unlock()
	}
	for i := range 3000 {
		if !s.setup.envCheck.TryLock() {
			t.Fatalf("round %d: the environment check lock is held", i)
		}
		if _, ok := s.checkEnv(map[string]string{"A": "x"}); !ok {
			t.Fatal("the check did not run")
		}
		if !s.setup.envCheck.TryLock() {
			t.Fatalf("round %d: the environment check returned while its lock was held", i)
		}
		s.setup.envCheck.Unlock()
	}
}
