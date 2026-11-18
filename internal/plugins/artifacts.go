package plugins

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

const (
	MaxActiveArtifacts = 4
	MaxArtifactKeys    = 64
)

var (
	ErrUnknownArtifact  = errors.New("no such artifact belongs to this plugin")
	ErrArtifactIdentity = errors.New("an artifact with this key is active with different options")
	ErrArtifactLimit    = errors.New("artifact limit reached: 4 active jobs or 64 retained keys per plugin")
)

// ArtifactStart is the exact argument of omo.artifact_start. Retry is required
// to rerun a failed/cancelled job with the same identity. No headers, arbitrary
// destinations, shell commands or other plugin namespaces can be selected.
type ArtifactStart struct {
	Key             string `json:"key"`
	URL             string `json:"url"`
	SHA256          string `json:"sha256,omitempty"`
	Format          string `json:"format"`
	StripComponents int    `json:"strip_components,omitempty"`
	Retry           bool   `json:"retry,omitempty"`
}

// ArtifactStatus is a snapshot. Path is present only after successful cache
// integrity checking/publication. No addresses, response text or raw errors
// reach this boundary. Bytes counts compressed download bytes, TotalBytes is
// zero if unknown. Verification distinguishes publisher pins from no checksum.
type ArtifactStatus struct {
	ID           string `json:"id"`
	Key          string `json:"key"`
	State        string `json:"state"`
	Phase        string `json:"phase"`
	Bytes        int64  `json:"bytes"`
	TotalBytes   int64  `json:"total_bytes"`
	Verification string `json:"verification"`
	Path         string `json:"path,omitempty"`
	Error        string `json:"error,omitempty"`
}
type artifactJob struct {
	plugin, identity string
	record           ArtifactStatus // guarded by artifactsMu
	cancel           context.CancelFunc
	done             chan struct{}
}
type artifactCompletion struct {
	Version      int    `json:"version"`
	Identity     string `json:"identity"`
	Generation   string `json:"generation"`
	TreeHash     string `json:"tree_hash"`
	Verification string `json:"verification"`
}

func validateArtifact(req *ArtifactStart) error {
	if !ValidName(req.Key) {
		return errors.New("artifact key must be 1 to 64 letters, digits, '.', '_' or '-', starting alphanumeric")
	}
	if len(req.URL) > 8192 || !artifactHTTPS(req.URL) {
		return errors.New("artifact URL must be HTTPS without user information or fragment, at most 8192 bytes")
	}
	req.SHA256 = strings.ToLower(req.SHA256)
	if req.SHA256 != "" {
		b, err := hex.DecodeString(req.SHA256)
		if len(req.SHA256) != 64 || err != nil || len(b) != sha256.Size {
			return errors.New("artifact sha256 must be 64 hexadecimal characters or empty")
		}
	}
	if req.Format != "tar.gz" && req.Format != "zip" {
		return errors.New("artifact format must be tar.gz or zip")
	}
	if req.StripComponents < 0 || req.StripComponents > 32 {
		return errors.New("artifact strip_components must be an integer from 0 to 32")
	}
	return nil
}
func artifactHTTPS(address string) bool {
	u, err := url.Parse(address)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" && u.Opaque == ""
}

// StartArtifact reserves one job under the plugin/key. Slow disk/network work
// always runs outside manager locks and the caller's hook context.
func (m *Manager) StartArtifact(ctx context.Context, plugin string, req ArtifactStart) (ArtifactStatus, error) {
	return m.startArtifact(ctx, plugin, nil, req)
}
func (m *Manager) startArtifact(ctx context.Context, name string, bound *plugin, req ArtifactStart) (ArtifactStatus, error) {
	if err := validateArtifact(&req); err != nil {
		return ArtifactStatus{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || ctx.Err() != nil {
		return ArtifactStatus{}, ErrClosed
	}
	var owner *plugin
	for _, e := range m.entries {
		if e.settings.Name == name {
			owner = e.p
			break
		}
	}
	if owner == nil || (bound != nil && bound != owner) || owner.ctx.Err() != nil {
		return ArtifactStatus{}, ErrUnknownPlugin
	}
	red := owner.redactor()
	artifactAddressSecrets(red, req.URL)
	if red.Clean(req.Key) != req.Key {
		return ArtifactStatus{}, errors.New("artifact key contains a configuration value")
	}
	identityReq := req
	identityReq.Retry = false
	raw, _ := json.Marshal(identityReq)
	sum := sha256.Sum256(raw)
	identity := hex.EncodeToString(sum[:])
	m.artifactsMu.Lock()
	defer m.artifactsMu.Unlock()
	if m.artifacts == nil {
		m.artifacts = map[string]map[string]*artifactJob{}
	}
	own := m.artifacts[name]
	if own == nil {
		own = map[string]*artifactJob{}
		m.artifacts[name] = own
	}
	old := own[req.Key]
	if old != nil {
		select {
		case <-old.done:
			if old.identity == identity && (old.record.State == "failed" || old.record.State == "cancelled") && !req.Retry {
				return old.record, nil
			}
		default:
			if old.identity != identity {
				return ArtifactStatus{}, ErrArtifactIdentity
			}
			return old.record, nil
		}
	}
	active := 0
	for _, j := range own {
		select {
		case <-j.done:
		default:
			active++
		}
	}
	if active >= MaxActiveArtifacts || (old == nil && len(own) >= MaxArtifactKeys) {
		return ArtifactStatus{}, ErrArtifactLimit
	}
	limits := defaultArtifactLimits()
	if m.artifactLimits != nil {
		limits = *m.artifactLimits
	}
	jobCtx, cancel := context.WithTimeout(m.ctx, limits.timeout)
	verification := "sha256"
	if req.SHA256 == "" {
		verification = "unverified"
	}
	j := &artifactJob{plugin: name, identity: identity, cancel: cancel, done: make(chan struct{}), record: ArtifactStatus{ID: rand.Text(), Key: req.Key, State: "running", Phase: "checking", Verification: verification}}
	own[req.Key] = j
	m.artifactsWG.Add(1)
	go m.installArtifact(jobCtx, j, req, limits)
	return j.record, nil
}

// ArtifactStatus reports in-memory state only; it never restores pending work.
// Call StartArtifact again before reusing ready cache to request a full check.
func (m *Manager) ArtifactStatus(plugin, key string) (ArtifactStatus, error) {
	m.artifactsMu.Lock()
	defer m.artifactsMu.Unlock()
	j := m.artifacts[plugin][key]
	if j == nil {
		return ArtifactStatus{}, ErrUnknownArtifact
	}
	return j.record, nil
}
func (m *Manager) artifactProgress(j *artifactJob, phase string, bytes, total int64) {
	m.artifactsMu.Lock()
	defer m.artifactsMu.Unlock()
	j.record.Phase = phase
	if bytes >= 0 {
		j.record.Bytes = bytes
	}
	if total >= 0 {
		j.record.TotalBytes = total
	}
}
func (m *Manager) installArtifact(ctx context.Context, j *artifactJob, req ArtifactStart, limits artifactLimits) {
	defer m.artifactsWG.Done()
	defer j.cancel()
	installed, err := m.installArtifactFiles(ctx, j, req, limits)
	m.artifactsMu.Lock()
	defer m.artifactsMu.Unlock()
	switch {
	case ctx.Err() != nil:
		j.record.State = "cancelled"
		j.record.Error = "artifact installation cancelled"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			j.record.State = "failed"
			j.record.Error = "artifact installation exceeded its time limit"
		}
	case err != nil:
		j.record.State = "failed"
		j.record.Error = err.Error()
	default:
		j.record.State = "ready"
		j.record.Path = installed
	}
	j.record.Phase = j.record.State
	close(j.done)
}
func artifactDirectory(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return errArchive
	}
	return nil
}
func (m *Manager) installArtifactFiles(ctx context.Context, j *artifactJob, req ArtifactStart, limits artifactLimits) (string, error) {
	cacheError := errors.New("artifact cache could not be read or written")
	data, err := m.dataDir(j.plugin)
	if err != nil {
		return "", cacheError
	}
	data, err = filepath.Abs(data)
	if err != nil {
		return "", cacheError
	}
	rootDir := filepath.Join(data, ".artifacts-v1")
	if err = artifactDirectory(rootDir); err != nil {
		return "", cacheError
	}
	keyDir := filepath.Join(rootDir, req.Key)
	if err = artifactDirectory(keyDir); err != nil {
		return "", cacheError
	}
	root, err := os.OpenRoot(keyDir)
	if err != nil {
		return "", cacheError
	}
	defer root.Close()
	reference := filepath.Join(keyDir, j.identity+".json")
	if raw, err := homefile.Read(reference, 4096); err == nil {
		var c artifactCompletion
		if json.Unmarshal(raw, &c) == nil && c.Version == 1 && c.Identity == j.identity && c.Verification == j.record.Verification && ValidName(c.Generation) && strings.HasPrefix(c.Generation, "g-") {
			generation := filepath.Join(keyDir, c.Generation)
			tree := filepath.Join(generation, "tree")
			if info, err := os.Lstat(generation); err == nil && info.IsDir() {
				complete, err := homefile.Read(filepath.Join(generation, "complete.json"), 4096)
				if err == nil && string(complete) == string(raw) {
					hash, err := artifactTreeHash(ctx, tree, limits)
					if err == nil && hash == c.TreeHash {
						return tree, nil
					}
				}
			}
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	stageName := ".stage-" + rand.Text()
	if err = root.Mkdir(stageName, 0o700); err != nil {
		return "", cacheError
	}
	defer root.RemoveAll(stageName)
	stage := filepath.Join(keyDir, stageName)
	archive := filepath.Join(stage, "download")
	if err = m.downloadArtifact(ctx, j, req, root, filepath.Join(stageName, "download"), limits); err != nil {
		return "", err
	}
	tree := filepath.Join(stage, "tree")
	if err = os.Mkdir(tree, 0o700); err != nil {
		return "", cacheError
	}
	m.artifactProgress(j, "extracting", -1, -1)
	if err = extractArtifact(ctx, archive, tree, req.Format, req.StripComponents, limits); err != nil {
		return "", errArchive
	}
	if err = root.Remove(filepath.Join(stageName, "download")); err != nil {
		return "", cacheError
	}
	hash, err := artifactTreeHash(ctx, tree, limits)
	if err != nil {
		return "", errArchive
	}
	generation := "g-" + rand.Text()
	c := artifactCompletion{Version: 1, Identity: j.identity, Generation: generation, TreeHash: hash, Verification: j.record.Verification}
	raw, _ := json.Marshal(c)
	if err = os.WriteFile(filepath.Join(stage, "complete.json"), raw, 0o600); err != nil {
		return "", cacheError
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	// The old reference/tree remains intact on every download/extraction failure.
	// Complete tree + manifest publish in one rename on the same filesystem.
	if err = root.Rename(stageName, generation); err != nil {
		return "", cacheError
	}
	if err = globalhome.WriteFileAtomic(reference, raw, 0o600); err != nil {
		return "", cacheError
	}
	return filepath.Join(keyDir, generation, "tree"), nil
}

type artifactDownloadWriter struct {
	m *Manager
	j *artifactJob
	w io.Writer
	n int64
}

func (w *artifactDownloadWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	w.m.artifactProgress(w.j, "downloading", w.n, -1)
	return n, err
}
func (m *Manager) downloadArtifact(ctx context.Context, j *artifactJob, req ArtifactStart, root *os.Root, dest string, limits artifactLimits) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.MaxResponseHeaderBytes = 64 << 10
	var rt http.RoundTripper = transport
	// Injection is for trusted transport configuration (for example fixture CAs),
	// never Lua policy. Cookies, auth headers and redirect policy are not copied.
	if m.o.ArtifactHTTP != nil && m.o.ArtifactHTTP.Transport != nil {
		rt = m.o.ArtifactHTTP.Transport
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: rt, Timeout: limits.timeout, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || !artifactHTTPS(r.URL.String()) {
			return errors.New("artifact redirect refused")
		}
		r.Header.Del("Referer")
		r.Header.Del("Authorization")
		r.Header.Del("Cookie")
		r.Header.Del("Proxy-Authorization")
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return errors.New("artifact request could not be prepared")
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "omo-plugin-artifact")
	m.artifactProgress(j, "downloading", 0, 0)
	response, err := client.Do(request)
	if err != nil {
		return errors.New("artifact HTTPS request failed or exceeded its redirect/time limit")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("artifact download returned an unsuccessful HTTP status")
	}
	if response.ContentLength > limits.compressed {
		return errors.New("artifact download exceeds its compressed byte limit")
	}
	total := response.ContentLength
	if total < 0 {
		total = 0
	}
	m.artifactProgress(j, "downloading", 0, total)
	f, err := root.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("artifact staging file could not be created")
	}
	h := sha256.New()
	writer := &artifactDownloadWriter{m: m, j: j, w: io.MultiWriter(f, h)}
	n, err := io.Copy(writer, io.LimitReader(response.Body, limits.compressed+1))
	closeErr := f.Close()
	if n > limits.compressed {
		return errors.New("artifact download exceeds its compressed byte limit")
	}
	if err != nil || closeErr != nil {
		return errors.New("artifact download was incomplete or could not be saved")
	}
	m.artifactProgress(j, "verifying", -1, -1)
	if req.SHA256 != "" && hex.EncodeToString(h.Sum(nil)) != req.SHA256 {
		return errors.New("artifact SHA-256 does not match the expected digest")
	}
	return nil
}
func (m *Manager) stopArtifacts(plugin string, forget bool) {
	m.artifactsMu.Lock()
	var jobs []*artifactJob
	for name, own := range m.artifacts {
		if plugin != "" && name != plugin {
			continue
		}
		for _, j := range own {
			j.cancel()
			jobs = append(jobs, j)
		}
	}
	m.artifactsMu.Unlock()
	for _, j := range jobs {
		<-j.done
	}
	if forget {
		m.artifactsMu.Lock()
		delete(m.artifacts, plugin)
		m.artifactsMu.Unlock()
	}
}
