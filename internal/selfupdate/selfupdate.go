// Package selfupdate checks GitHub releases and installs a verified omo binary.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const latestReleaseURL = "https://api.github.com/repos/scolastico-dev/one-man-office/releases/latest"

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type Release struct {
	Tag    string  `json:"tag_name"`
	Assets []Asset `json:"assets"`
}

type Client struct {
	HTTP   *http.Client
	APIURL string
	GOOS   string
	GOARCH string
}

func (c Client) defaults() Client {
	if c.HTTP == nil {
		c.HTTP = http.DefaultClient
	}
	if c.APIURL == "" {
		c.APIURL = latestReleaseURL
	}
	if c.GOOS == "" {
		c.GOOS = runtime.GOOS
	}
	if c.GOARCH == "" {
		c.GOARCH = runtime.GOARCH
	}
	return c
}

func (c Client) Latest(ctx context.Context) (Release, error) {
	c = c.defaults()
	var release Release
	if err := c.getJSON(ctx, c.APIURL, &release); err != nil {
		return release, err
	}
	if strings.TrimSpace(release.Tag) == "" {
		return release, fmt.Errorf("latest release has no tag")
	}
	return release, nil
}

func (c Client) getJSON(ctx context.Context, url string, out any) error {
	resp, err := c.get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c Client) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "omo-self-update")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

// IsNewer compares release-style semantic versions. Development builds and
// non-semantic tags are deliberately not auto-updated.
func IsNewer(current, latest string) bool {
	a, okA := versionParts(current)
	b, okB := versionParts(latest)
	if !okA || !okB {
		return false
	}
	for i := range a {
		if b[i] != a[i] {
			return b[i] > a[i]
		}
	}
	return false
}

func versionParts(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func (c Client) Install(ctx context.Context, release Release, target string) error {
	c = c.defaults()
	archiveName := "omo-" + c.GOOS + "-" + c.GOARCH + ".tar.gz"
	binaryName := "omo"
	if c.GOOS == "windows" {
		archiveName = "omo-windows-" + c.GOARCH + ".zip"
		binaryName = "omo.exe"
	}
	archiveURL, checksumsURL := "", ""
	for _, asset := range release.Assets {
		switch asset.Name {
		case archiveName:
			archiveURL = asset.URL
		case "SHA256SUMS":
			checksumsURL = asset.URL
		}
	}
	if archiveURL == "" || checksumsURL == "" {
		return fmt.Errorf("release %s does not contain %s and SHA256SUMS", release.Tag, archiveName)
	}
	archive, err := c.download(ctx, archiveURL)
	if err != nil {
		return fmt.Errorf("download %s: %w", archiveName, err)
	}
	checksums, err := c.download(ctx, checksumsURL)
	if err != nil {
		return fmt.Errorf("download SHA256SUMS: %w", err)
	}
	want, err := checksumFor(checksums, archiveName)
	if err != nil {
		return err
	}
	got := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(got[:]), want) {
		return fmt.Errorf("checksum verification failed for %s", archiveName)
	}
	binary, err := extractBinary(archiveName, archive, binaryName)
	if err != nil {
		return err
	}
	return installBinary(target, binary)
}

func (c Client) download(ctx context.Context, url string) ([]byte, error) {
	resp, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

func checksumFor(raw []byte, name string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		candidate := strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./")
		if candidate == name {
			if len(fields[0]) != sha256.Size*2 {
				break
			}
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("release checksum for %s was not found", name)
}

func extractBinary(archiveName string, raw []byte, binaryName string) ([]byte, error) {
	if strings.HasSuffix(archiveName, ".zip") {
		zr, err := zip.NewReader(strings.NewReader(string(raw)), int64(len(raw)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) != binaryName {
				continue
			}
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer r.Close()
			return io.ReadAll(r)
		}
	} else {
		gz, err := gzip.NewReader(strings.NewReader(string(raw)))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if filepath.Base(h.Name) == binaryName && h.Typeflag == tar.TypeReg {
				return io.ReadAll(tr)
			}
		}
	}
	return nil, fmt.Errorf("%s does not contain %s", archiveName, binaryName)
}

func installBinary(target string, binary []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".omo-update-*")
	if err != nil {
		return fmt.Errorf("stage update next to %s: %w", target, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceExecutable(tmpName, target)
}
