package main

import (
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// x509.SetFallbackRoots can be called once in a process and never undone, so a
// test that sets it cannot run twice in one test binary (-count=2, shuffle),
// and the process that set it keeps the trust. The trust assertions therefore
// run in a process of their own: this test binary started again as a probe.
const (
	probeURL     = "OMOCA_PROBE_URL"
	probeCAFile  = "OMOCA_PROBE_CA_FILE"
	probeExitOK  = 0
	probeExitCA  = 3 // the certificate was refused as signed by an unknown authority
	probeExitErr = 4 // anything else
)

func TestMain(m *testing.M) {
	if url := os.Getenv(probeURL); url != "" {
		os.Exit(probe(url, os.Getenv(probeCAFile)))
	}
	os.Exit(m.Run())
}

// probe is the helper process: it sets the fallback roots from caFile (none when
// empty), asks url, and reports through its exit code.
func probe(url, caFile string) int {
	if caFile != "" {
		pool, err := fixtureRoots(caFile)
		if err != nil {
			return probeExitErr
		}
		x509.SetFallbackRoots(pool)
	}
	resp, err := http.Get(url)
	if err == nil {
		_ = resp.Body.Close()
		return probeExitOK
	}
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &unknown) {
		return probeExitCA
	}
	return probeExitErr
}

// ask runs the probe in a new process and returns its exit code.
func ask(t *testing.T, url, caFile string) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), probeURL+"="+url, probeCAFile+"="+caFile)
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return exit.ExitCode()
}

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
	if got := ask(t, trusted.URL, ""); got != probeExitCA {
		t.Fatalf("with no fixture roots the server was not refused as an unknown authority: exit %d", got)
	}
	if got := ask(t, trusted.URL, caFile); got != probeExitOK {
		t.Fatalf("the fixture's own authority was refused: exit %d", got)
	}
	if got := ask(t, stranger.URL, caFile); got != probeExitCA {
		t.Fatalf("a certificate of another authority was not refused as unknown: exit %d", got)
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
