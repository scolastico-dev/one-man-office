package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func artifactRequest(url string, raw []byte) ArtifactStart {
	h := sha256.Sum256(raw)
	return ArtifactStart{Key: "tool", URL: url, SHA256: hex.EncodeToString(h[:]), Format: "tar.gz", StripComponents: 1}
}
func waitArtifact(t *testing.T, m *Manager, p, key string) ArtifactStatus {
	t.Helper()
	var s ArtifactStatus
	eventually(t, "artifact terminal", func() bool {
		var err error
		s, err = m.ArtifactStatus(p, key)
		if err != nil {
			t.Fatal(err)
		}
		return s.State == "ready" || s.State == "failed" || s.State == "cancelled"
	})
	return s
}
func TestArtifactsAsyncDedupCacheAndIntegrity(t *testing.T) {
	e := taskPlugin(t)
	raw := tarFixture(t, archiveMember{name: "pkg/run", body: "fixture", mode: 0o755})
	var requests atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Write(raw)
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	e.m.o.ArtifactHTTP = server.Client()
	req := artifactRequest(server.URL, raw)
	first, err := e.m.StartArtifact(t.Context(), "p", req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != "" || first.ID == "" {
		t.Fatalf("premature path or missing ID: %+v", first)
	}
	<-started
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			s, err := e.m.StartArtifact(t.Context(), "p", req)
			if err != nil || s.ID != first.ID {
				t.Errorf("dedup: %+v %v", s, err)
			}
		})
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatal("parallel downloads")
	}
	close(release)
	ready := waitArtifact(t, e.m, "p", "tool")
	if ready.State != "ready" || ready.Verification != "sha256" || ready.Path == "" || ready.Bytes != int64(len(raw)) {
		t.Fatalf("install: %+v", ready)
	}
	if _, err = e.m.ArtifactStatus("q", "tool"); err == nil {
		t.Fatal("cross-plugin status")
	}
	old := ready.Path
	// Even in-memory ready cache must be rechecked on explicit reuse.
	for _, tamper := range []string{"none", "content", "missing", "extra", "metadata", "mode"} {
		t.Run(tamper, func(t *testing.T) {
			if tamper == "mode" && os.PathSeparator == '\\' {
				return
			}
			switch tamper {
			case "content":
				os.WriteFile(filepath.Join(old, "run"), []byte("changed"), 0o700)
			case "missing":
				os.Remove(filepath.Join(old, "run"))
			case "extra":
				os.WriteFile(filepath.Join(old, "extra"), []byte("extra"), 0o600)
			case "metadata":
				os.Remove(filepath.Join(filepath.Dir(old), "complete.json"))
			case "mode":
				os.Chmod(filepath.Join(old, "run"), 0o600)
			}
			s, err := e.m.StartArtifact(t.Context(), "p", req)
			if err != nil {
				t.Fatal(err)
			}
			if s.Path != "" {
				t.Fatal("path exposed before async validation")
			}
			s = waitArtifact(t, e.m, "p", "tool")
			if s.State != "ready" {
				t.Fatalf("cache reuse: %+v", s)
			}
			old = s.Path
		})
	}
	if requests.Load() != 6 {
		t.Fatalf("tampered caches not replaced: %d", requests.Load())
	}
	// New manager can reuse completion, never resumes a queue.
	other := New(Options{Root: e.root, ArtifactHTTP: server.Client()})
	defer other.Close()
	if errs := other.Load([]api.PluginSettings{enabled("p", nil)}); len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, err := other.ArtifactStatus("p", "tool"); err == nil {
		t.Fatal("restored job queue")
	}
	if _, err := other.StartArtifact(t.Context(), "p", req); err != nil {
		t.Fatal(err)
	}
	reused := waitArtifact(t, other, "p", "tool")
	if reused.State != "ready" || reused.Path != old || requests.Load() != 6 {
		t.Fatalf("restart cache: %+v requests %d", reused, requests.Load())
	}
}
func TestArtifactsIdentityFailureRetryAndUnverified(t *testing.T) {
	e := taskPlugin(t)
	raw := tarFixture(t, archiveMember{name: "pkg/file", body: "good"})
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write(raw) }))
	defer server.Close()
	e.m.o.ArtifactHTTP = server.Client()
	req := artifactRequest(server.URL, raw)
	e.m.StartArtifact(t.Context(), "p", req)
	good := waitArtifact(t, e.m, "p", "tool")
	if good.State != "ready" {
		t.Fatal(good)
	}
	req.SHA256 = strings.Repeat("0", 64)
	s, err := e.m.StartArtifact(t.Context(), "p", req)
	if err != nil || s.ID == good.ID {
		t.Fatal("identity reused", err)
	}
	bad := waitArtifact(t, e.m, "p", "tool")
	if bad.State != "failed" || bad.Path != "" || !strings.Contains(bad.Error, "SHA-256") {
		t.Fatal(bad)
	}
	s, _ = e.m.StartArtifact(t.Context(), "p", req)
	if s.ID != bad.ID || requests.Load() != 2 {
		t.Fatal("implicit retry")
	}
	req.Retry = true
	e.m.StartArtifact(t.Context(), "p", req)
	if s = waitArtifact(t, e.m, "p", "tool"); s.ID == bad.ID || s.State != "failed" {
		t.Fatal("explicit retry", s)
	}
	b, err := os.ReadFile(filepath.Join(good.Path, "file"))
	if err != nil || string(b) != "good" {
		t.Fatal("failed install changed good tree")
	}
	req.SHA256 = ""
	req.Retry = false
	e.m.StartArtifact(t.Context(), "p", req)
	s = waitArtifact(t, e.m, "p", "tool")
	if s.State != "ready" || s.Verification != "unverified" {
		t.Fatal(s)
	}
	for _, change := range []func(*ArtifactStart){func(r *ArtifactStart) { r.URL = "http://example.test/tool" }, func(r *ArtifactStart) { r.SHA256 = "malformed" }, func(r *ArtifactStart) { r.Key = "../q" }, func(r *ArtifactStart) { r.Format = "shell" }, func(r *ArtifactStart) { r.StripComponents = -1 }} {
		r := req
		change(&r)
		if _, err = e.m.StartArtifact(t.Context(), "p", r); err == nil {
			t.Fatal("bad request accepted")
		}
	}
}
func TestArtifactsProgressNetworkBoundsAndSecretErrors(t *testing.T) {
	raw := tarFixture(t, archiveMember{name: "pkg/file", body: strings.Repeat("payload", 500)})
	for _, kind := range []string{"progress", "size", "stream-size", "expanded", "timeout", "redirect", "downgrade", "cross-host", "bad-status", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			e := taskPlugin(t)
			entered := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			second := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
					t.Error("credentials forwarded")
				}
				w.Write(raw)
			}))
			defer second.Close()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "progress":
					w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
					w.Write(raw[:8])
					w.(http.Flusher).Flush()
					close(entered)
					select {
					case <-release:
						w.Write(raw[8:])
					case <-r.Context().Done():
					}
				case "stream-size":
					w.(http.Flusher).Flush()
					w.Write(raw)
				case "timeout":
					<-r.Context().Done()
				case "redirect":
					http.Redirect(w, r, "/again?token=redirect-secret", http.StatusFound)
				case "downgrade":
					http.Redirect(w, r, "http://example.test/?token=redirect-secret", http.StatusFound)
				case "cross-host":
					http.Redirect(w, r, strings.Replace(second.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
				case "bad-status":
					w.WriteHeader(403)
					io.WriteString(w, "response-secret")
				case "truncated":
					w.Header().Set("Content-Length", fmt.Sprint(len(raw)+10))
					w.Write(raw)
				default:
					w.Write(raw)
				}
			}))
			defer server.Close()
			e.m.o.ArtifactHTTP = server.Client()
			e.m.o.ArtifactHTTP.Transport.(*http.Transport).TLSClientConfig.ServerName = "example.com"
			limits := defaultArtifactLimits()
			if kind == "size" || kind == "stream-size" {
				limits.compressed = 8
			}
			if kind == "expanded" {
				limits.expanded = 8
			}
			if kind == "timeout" {
				limits.timeout = 50 * time.Millisecond
			}
			e.m.artifactLimits = &limits
			req := artifactRequest(server.URL+"/?token=url-secret", raw)
			e.m.StartArtifact(t.Context(), "p", req)
			if kind == "progress" {
				<-entered
				eventually(t, "download bytes", func() bool {
					s, _ := e.m.ArtifactStatus("p", "tool")
					return s.Bytes == 8 && s.Phase == "downloading" && s.TotalBytes == int64(len(raw)) && s.Path == ""
				})
				e.m.Close()
				s := waitArtifact(t, e.m, "p", "tool")
				if s.State != "cancelled" {
					t.Fatal(s)
				}
				return
			}
			s := waitArtifact(t, e.m, "p", "tool")
			if kind == "cross-host" {
				if s.State != "ready" {
					t.Fatal(s)
				}
			} else if s.State != "failed" || s.Error == "" || s.Path != "" {
				t.Fatalf("expected bounded failure: %+v", s)
			}
			b, _ := json.Marshal(s)
			for _, secret := range []string{"url-secret", "redirect-secret", "response-secret"} {
				if bytes.Contains(b, []byte(secret)) {
					t.Fatalf("leaked secret: %s", b)
				}
			}
		})
	}
}
func TestArtifactsLifecycleCancellationAndConfigRefresh(t *testing.T) {
	for _, kind := range []string{"disable", "remove", "replace", "close", "config"} {
		t.Run(kind, func(t *testing.T) {
			e := taskPlugin(t)
			entered := make(chan struct{})
			cancelled := make(chan struct{})
			raw := tarFixture(t, archiveMember{name: "pkg/file", body: "ok"})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(cancelled) }))
			defer server.Close()
			e.m.o.ArtifactHTTP = server.Client()
			_, err := e.m.StartArtifact(t.Context(), "p", artifactRequest(server.URL, raw))
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			switch kind {
			case "disable":
				e.load(api.PluginSettings{Name: "p", Enabled: false}, enabled("q", nil))
			case "remove":
				e.load(enabled("q", nil))
			case "replace":
				e.plugin("p", manifestOf(), nil)
				os.WriteFile(filepath.Join(e.root, "p", ManifestFile), []byte(`{"description":"replacement","hooks":[]}`), 0o600)
				e.load(enabled("p", nil), enabled("q", nil))
			case "close":
				e.m.Close()
			case "config":
				e.load(enabled("p", map[string]any{"setting": "value"}), enabled("q", nil))
				s, _ := e.m.ArtifactStatus("p", "tool")
				if s.State == "cancelled" {
					t.Fatal("config killed install")
				}
				e.m.Close()
			}
			select {
			case <-cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("download survived lifecycle")
			}
			if kind != "remove" {
				s := waitArtifact(t, e.m, "p", "tool")
				if s.State != "cancelled" || s.Path != "" {
					t.Fatal(s)
				}
			}
			files, err := filepath.Glob(filepath.Join(e.root, ".data", "p", ".artifacts-v1", "*", ".stage-*"))
			if err != nil || len(files) > 0 {
				t.Fatal("left staging", files, err)
			}
		})
	}
}

func TestArtifactsCallerCancellationIdentityAndLimits(t *testing.T) {
	e := taskPlugin(t)
	raw := tarFixture(t, archiveMember{name: "pkg/file", body: "ok"})
	entered := make(chan struct{}, 8)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-r.Context().Done() }))
	defer server.Close()
	e.m.o.ArtifactHTTP = server.Client()
	req := artifactRequest(server.URL, raw)
	ctx, cancel := context.WithCancel(t.Context())
	first, err := e.m.StartArtifact(ctx, "p", req)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	cancel()
	s, err := e.m.ArtifactStatus("p", req.Key)
	if err != nil || s.State != "running" {
		t.Fatal("hook cancellation killed independent job", s, err)
	}
	changed := req
	changed.StripComponents = 0
	if _, err = e.m.StartArtifact(t.Context(), "p", changed); !errors.Is(err, ErrArtifactIdentity) {
		t.Fatal("active identity conflict", err)
	}
	for i := 1; i < MaxActiveArtifacts; i++ {
		r := req
		r.Key = fmt.Sprintf("tool%d", i)
		if _, err = e.m.StartArtifact(t.Context(), "p", r); err != nil {
			t.Fatal(err)
		}
	}
	extra := req
	extra.Key = "overflow"
	if _, err = e.m.StartArtifact(t.Context(), "p", extra); !errors.Is(err, ErrArtifactLimit) {
		t.Fatal("active job limit", err)
	}
	// A different plugin owns a separate namespace and allowance.
	other, err := e.m.StartArtifact(t.Context(), "q", req)
	if err != nil || other.ID == first.ID {
		t.Fatal("plugin namespace shared", other, err)
	}
	e.m.Close()
	if _, err = e.m.StartArtifact(t.Context(), "p", req); !errors.Is(err, ErrClosed) {
		t.Fatal("start after shutdown", err)
	}
}
func TestArtifactsCacheRefusesLinksAndKeepsUnrelatedData(t *testing.T) {
	e := taskPlugin(t)
	raw := tarFixture(t, archiveMember{name: "pkg/file", body: "ok"})
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write(raw) }))
	defer server.Close()
	e.m.o.ArtifactHTTP = server.Client()
	req := artifactRequest(server.URL, raw)
	e.m.StartArtifact(t.Context(), "p", req)
	s := waitArtifact(t, e.m, "p", "tool")
	if s.State != "ready" {
		t.Fatal(s)
	}
	unrelated := filepath.Join(e.root, ".data", "p", "user-data")
	if err := os.WriteFile(unrelated, []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		outside := filepath.Join(t.TempDir(), "outside")
		os.WriteFile(outside, []byte("outside"), 0o600)
		os.Remove(filepath.Join(s.Path, "file"))
		if err := os.Symlink(outside, filepath.Join(s.Path, "file")); err != nil {
			t.Fatal(err)
		}
		e.m.StartArtifact(t.Context(), "p", req)
		s = waitArtifact(t, e.m, "p", "tool")
		if s.State != "ready" || requests.Load() != 2 {
			t.Fatal("symlink cache reused", s)
		}
		b, _ := os.ReadFile(outside)
		if string(b) != "outside" {
			t.Fatal("outside target changed")
		}
	}
	// A partial generation is never enough without matching completion+reference.
	complete := filepath.Join(filepath.Dir(s.Path), "complete.json")
	if err := os.WriteFile(complete, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	e.m.StartArtifact(t.Context(), "p", req)
	s = waitArtifact(t, e.m, "p", "tool")
	if s.State != "ready" {
		t.Fatal(s)
	}
	b, _ := os.ReadFile(unrelated)
	if string(b) != "retain" {
		t.Fatal("unrelated plugin data changed")
	}
}
