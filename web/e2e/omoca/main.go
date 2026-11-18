// Command omoca is the browser tests' omo for the specs whose local HTTPS
// fixture has its own throw-away certificate authority. It is the same
// command line as cmd/omo (cli.Explain(cli.Root(version).Execute())) and no
// product code differs: before it delegates, it puts the one certificate
// authority named by the file in E2E_FIXTURE_CA_FILE into Go's fallback roots
// and forces Go's pure-Go verifier to use them. Go reads SSL_CERT_FILE on
// Linux and the BSDs only; the fallback roots work the same on every platform.
// Certificate checks stay on: a server with any other certificate is still
// refused. The tests build it next to the real binary and use it only for
// those specs; nothing ships it.
//
//go:debug x509usefallbackroots=1
package main

import (
	"crypto/x509"
	"fmt"
	"os"

	"github.com/scolastico-dev/one-man-office/internal/cli"
)

// version is stamped by the build like cmd/omo's.
var version = "dev"

// EnvCAFile names the PEM file holding the fixture's certificate authority.
const EnvCAFile = "E2E_FIXTURE_CA_FILE"

// fixtureRoots reads the PEM file into a pool holding only that authority.
func fixtureRoots(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s holds no certificate", path)
	}
	return pool, nil
}

func main() {
	path := os.Getenv(EnvCAFile)
	if path == "" {
		fmt.Fprintln(os.Stderr, "omoca: refusing to run without", EnvCAFile)
		os.Exit(2)
	}
	pool, err := fixtureRoots(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "omoca:", err)
		os.Exit(2)
	}
	x509.SetFallbackRoots(pool)
	if err := cli.Explain(cli.Root(version).Execute()); err != nil {
		fmt.Fprintln(os.Stderr, "omo:", err)
		os.Exit(1)
	}
}
