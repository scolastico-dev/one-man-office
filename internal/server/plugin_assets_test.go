package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func frontendRecorder(t *testing.T, s *Server, b *browser) (string, string) {
	t.Helper()
	installRecorder(t, s, b)
	dir := filepath.Join(s.opts.Layout.Plugins, "rec")
	if err := os.MkdirAll(filepath.Join(dir, "public/images"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"entry.js": "document.currentScript.omo;", "public/images/logo.svg": "<svg/>", "secret.lua": "private"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := recordManifest("rec")
	m["frontend"] = map[string]string{"script": "entry.js", "assets": "public"}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	s.reloadPlugins(true)
	return dir, "/plugin-assets/rec/" + s.pluginInfo("rec").ContentID + "/"
}

func TestPluginAssetsPublicBoundedAndEnabled(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	dir, base := frontendRecorder(t, s, b)
	for _, tc := range []struct{ path, content, mime string }{{"entry.js", "document.currentScript.omo;", "text/javascript"}, {"public/images/logo.svg", "<svg/>", "image/svg+xml"}} {
		resp := b.do("GET", base+tc.path, nil, nil)
		wantStatus(t, resp.StatusCode, http.StatusOK)
		if got := respText(t, resp); got != tc.content {
			t.Fatalf("content %q", got)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), tc.mime) {
			t.Fatal(resp.Header)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("disabled assets must not be reusable from cache")
		}
		if strings.Contains(resp.Header.Get("Content-Security-Policy"), "unsafe-eval") {
			t.Fatal("CSP relaxed")
		}
	}
	for _, p := range []string{"secret.lua", "plugin.json", "public", "public/.git/config", "public/%2e%2e/secret.lua", "public/%2e%2e%2fsecret.lua", "public/%5c..%5csecret.lua", "public/C:secret", "public/settings.yaml", "../rec.yaml"} {
		resp := b.do("GET", base+p, nil, nil)
		wantStatus(t, resp.StatusCode, http.StatusNotFound)
		resp.Body.Close()
	}
	// Content identity also refuses hand-edited bytes before a runtime reload.
	if err := os.WriteFile(filepath.Join(dir, "entry.js"), []byte("updated"), 0600); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, b.do("GET", base+"entry.js", nil, nil).StatusCode, http.StatusNotFound)
	s.reloadPlugins(true)
	next := "/plugin-assets/rec/" + s.pluginInfo("rec").ContentID + "/entry.js"
	if next == base+"entry.js" {
		t.Fatal("unchanged identity")
	}
	wantStatus(t, b.do("GET", next, nil, nil).StatusCode, http.StatusOK)
	p, _ := s.settings.Plugin("rec")
	p.Enabled = false
	if err := s.settings.PutPluginIf(p.Revision, p); err != nil {
		t.Fatal(err)
	}
	s.reloadPlugins(true)
	wantStatus(t, b.do("GET", next, nil, nil).StatusCode, http.StatusNotFound)
}

func TestPluginAssetsRefuseReplacedLinks(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	dir, base := frontendRecorder(t, s, b)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "logo.svg"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "public/images"), filepath.Join(dir, "images-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "public/images")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	wantStatus(t, b.do("GET", base+"public/images/logo.svg", nil, nil).StatusCode, http.StatusNotFound)
}

func TestPluginAssetsRefuseInvalidRemovedAndOversizedFiles(t *testing.T) {
	for _, kind := range []string{"invalid", "removed", "directory", "large-script", "large-asset", "leaf-link", "root-link"} {
		t.Run(kind, func(t *testing.T) {
			isolateGit(t)
			s := newTestServer(t)
			b := newBrowser(t, s)
			dir, base := frontendRecorder(t, s, b)
			entry := filepath.Join(dir, "entry.js")
			requested := "entry.js"
			var err error
			switch kind {
			case "invalid":
				err = os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{}`), 0600)
				s.reloadPlugins(true)
			case "removed":
				err = os.Rename(dir, dir+"-removed")
			case "directory":
				err = os.Rename(entry, entry+"-old")
				if err == nil {
					err = os.Mkdir(entry, 0700)
				}
			case "large-script":
				err = os.WriteFile(entry, []byte(strings.Repeat("x", (1<<20)+1)), 0600)
			case "large-asset":
				requested = "public/huge"
				var f *os.File
				f, err = os.Create(filepath.Join(dir, "public/huge"))
				if err == nil {
					err = f.Truncate(64 << 20)
					f.Close()
				}
			case "leaf-link":
				err = os.Rename(entry, entry+"-old")
				if err == nil {
					err = os.Symlink(entry+"-old", entry)
				}
				if err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "root-link":
				err = os.Rename(dir, dir+"-old")
				if err == nil {
					err = os.Symlink(dir+"-old", dir)
				}
				if err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			wantStatus(t, b.do("GET", base+requested, nil, nil).StatusCode, http.StatusNotFound)
		})
	}
}

func TestPluginAssetsBasicModeIsPublicButSettingsRemainPrivate(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	_, base := frontendRecorder(t, s, b)
	s.opts.Config.Auth = "basic"
	s.opts.Config.BasicAuth = "user:password"
	wantStatus(t, b.do("GET", base+"entry.js", nil, nil).StatusCode, http.StatusOK)
	wantStatus(t, b.do("HEAD", base+"entry.js", nil, nil).StatusCode, http.StatusOK)
	wantStatus(t, b.do("GET", "/api/plugins", nil, nil).StatusCode, http.StatusUnauthorized)
}

func TestPluginAssetReadsOnlyRequestedFile(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	dir, base := frontendRecorder(t, s, b)
	// An unrelated known asset becomes oversized. Serving the entry must not
	// re-read that asset or rewalk/hash the entire tree for each request.
	f, err := os.OpenFile(filepath.Join(dir, "public/images/logo.svg"), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(64 << 20)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	resp := b.do("GET", base+"entry.js", nil, nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := respText(t, resp); got != "document.currentScript.omo;" {
		t.Fatalf("entry %q", got)
	}
	wantStatus(t, b.do("GET", base+"public/images/logo.svg", nil, nil).StatusCode, http.StatusNotFound)
	// A same-size edit is refused too, without requiring metadata changes.
	if err := os.WriteFile(filepath.Join(dir, "entry.js"), []byte(strings.Repeat("x", len("document.currentScript.omo;"))), 0600); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, b.do("GET", base+"entry.js", nil, nil).StatusCode, http.StatusNotFound)
}
