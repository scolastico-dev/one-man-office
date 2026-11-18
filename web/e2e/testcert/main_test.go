package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func TestGeneratedCertificateIsValidForLoopbackAndSelfSigned(t *testing.T) {
	now := time.Now()
	certPEM, keyPEM, err := generate(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: "127.0.0.1", CurrentTime: now}); err != nil {
		t.Fatalf("the certificate does not verify for 127.0.0.1 with itself as the root: %v", err)
	}
}
