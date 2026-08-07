package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestIsNewer(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		want            bool
	}{
		{"0.1.1", "v0.1.2", true},
		{"v1.9.0", "1.10.0", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.4", "1.2.3", false},
		{"dev", "1.2.3", false},
	} {
		if got := IsNewer(tc.current, tc.latest); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestLatestAndInstallVerifiedRelease(t *testing.T) {
	binary := []byte("new omo binary")
	archive := tarGzip(t, "omo", binary)
	sum := sha256.Sum256(archive)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		switch r.URL.String() {
		case "https://test/latest":
			body = []byte(`{"tag_name":"v1.2.3","assets":[{"name":"omo-linux-amd64.tar.gz","browser_download_url":"https://test/archive"},{"name":"SHA256SUMS","browser_download_url":"https://test/checksums"}]}`)
		case "https://test/archive":
			body = archive
		case "https://test/checksums":
			body = []byte(fmt.Sprintf("%x  ./omo-linux-amd64.tar.gz\n", sum))
		default:
			return response(http.StatusNotFound, nil), nil
		}
		return response(http.StatusOK, body), nil
	})}
	c := Client{HTTP: client, APIURL: "https://test/latest", GOOS: "linux", GOARCH: "amd64"}
	release, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "omo")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := c.Install(context.Background(), release, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, binary) {
		t.Fatalf("installed %q, want %q", got, binary)
	}
}

func TestInstallRejectsBadChecksum(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == "https://test/archive" {
			return response(http.StatusOK, []byte("not trusted")), nil
		}
		return response(http.StatusOK, []byte("0000000000000000000000000000000000000000000000000000000000000000  ./omo-linux-amd64.tar.gz\n")), nil
	})}
	c := Client{HTTP: client, GOOS: "linux", GOARCH: "amd64"}
	release := Release{Tag: "v1.2.3", Assets: []Asset{
		{Name: "omo-linux-amd64.tar.gz", URL: "https://test/archive"},
		{Name: "SHA256SUMS", URL: "https://test/checksums"},
	}}
	target := filepath.Join(t.TempDir(), "omo")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := c.Install(context.Background(), release, target); err == nil {
		t.Fatal("bad checksum must be rejected")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "old" {
		t.Fatalf("bad update changed target to %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func tarGzip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
