package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// serve starts a TLS server with its own certificate and returns it with a
// file holding that certificate as PEM.
func serve(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv, pemBytes := newServer(t)
	file := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(file, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return srv, file
}

func TestFixtureAuthorityIsTrustedAndNothingElseIs(t *testing.T) {
	trusted, caFile := serve(t)
	stranger, _ := serve(t)
	pool, err := fixtureRoots(caFile)
	if err != nil {
		t.Fatal(err)
	}
	// Both servers are refused by the default client first: nothing trusts them yet.
	for _, srv := range []*httptest.Server{trusted, stranger} {
		if _, err := http.Get(srv.URL); err == nil {
			t.Fatalf("%s was trusted before the fixture roots were set", srv.URL)
		}
	}
	setFallback(pool)
	resp, err := http.Get(trusted.URL)
	if err != nil {
		t.Fatalf("the fixture's own authority was refused: %v", err)
	}
	_ = resp.Body.Close()
	if _, err := http.Get(stranger.URL); err == nil {
		t.Fatal("a certificate of another authority was accepted")
	}
}

func TestFixtureRootsRefuseAFileWithoutCertificate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(file, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureRoots(file); err == nil {
		t.Fatal("a file without a certificate was accepted")
	}
	if _, err := fixtureRoots(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("a missing file was accepted")
	}
}
