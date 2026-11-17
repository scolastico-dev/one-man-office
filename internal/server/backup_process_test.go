//go:build !windows

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/homezip"
)

// TestUsedHomeIsClassified runs omo with an agent at work, a plugin with
// data, an image, an export with the database, a settings import and a
// database import followed by a restart, and then fails for any file or
// folder of the home that is neither exported nor named as not exported:
// a new kind of file in the home must be sorted into one of the lists.
func TestUsedHomeIsClassified(t *testing.T) {
	w := startProcessWorld(t, "ready\nhang", 5*time.Second)
	repo := pluginRepo(t, recordManifest("rec", allBoardEvents...))
	w.call("POST", "/api/plugins", community(api.PluginSettings{Name: "rec", Source: repo, Enabled: true}), nil)
	n := w.approveTask()
	w.developer(n, api.SessionRunning)
	w.upload(tp(n)+"/images", testPNG(t))
	w.call("PUT", "/api/prompts/developer-instructions", api.PromptUpdate{Text: "Before the export."}, nil)

	data := w.download("/api/settings/export?database=true")
	w.call("PUT", "/api/prompts/developer-instructions", api.PromptUpdate{Text: "After the export."}, nil)
	w.call("POST", "/api/agents/stop-all", map[string]any{}, nil)
	var plan api.ImportPlan
	if err := json.Unmarshal(w.upload("/api/settings/import", data), &plan); err != nil {
		t.Fatal(err)
	}
	var res api.ImportResult
	w.call("POST", "/api/settings/import/apply", api.ImportApply{ID: plan.ID, Server: true, Database: true}, &res)
	if !res.DatabasePending || res.Backup == "" {
		t.Fatalf("result %+v", res)
	}
	if err := w.stop(time.Minute); err != nil {
		t.Fatal(err)
	}
	// The next start swaps the database.
	s := openServer(t, w.layout, testConfig())
	if text, _, _ := s.settings.Prompt("developer-instructions"); text != "Before the export." {
		t.Fatalf("prompt %q", text)
	}
	if paused, _ := s.board.Paused(context.Background()); paused {
		t.Fatal("the imported database is paused; it was not when exported")
	}
	s.Close()
	if moved, _ := filepath.Glob(filepath.Join(w.layout.Root, homezip.BackupFolder, "*-database", "omo.db")); len(moved) != 1 {
		t.Fatalf("old database kept at %v", moved)
	}

	left, err := homezip.Unclassified(w.layout.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) > 0 {
		t.Fatalf("not sorted into homezip.Exported or homezip.NotExported: %q", left)
	}
}

// upload posts raw bytes as the user through the socket and returns the
// answer.
func (w *processWorld) upload(path string, body []byte) []byte {
	w.t.Helper()
	req, _ := http.NewRequest("POST", "http://omo"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(ImageNameHeader, "shot.png")
	resp, err := socketClient(w.layout).Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		w.t.Fatalf("POST %s: %d %s", path, resp.StatusCode, raw)
	}
	return raw
}

// download reads path as the user through the socket.
func (w *processWorld) download(path string) []byte {
	w.t.Helper()
	resp, err := socketClient(w.layout).Get("http://omo" + path)
	if err != nil {
		w.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		w.t.Fatalf("GET %s: %d %s", path, resp.StatusCode, raw)
	}
	return raw
}
