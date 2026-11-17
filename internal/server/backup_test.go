package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/homezip"
)

var backupRoutes = []extensionRoute{
	{"GET /api/settings/export", nil},
	{"POST /api/settings/import", nil},
	{"POST /api/settings/import/apply", api.ImportApply{ID: strings.Repeat("a", 32)}},
}

// TestBackupRoutesAreUserOnly: the zip holds tokens and environment values
// and an import rewrites code that runs as the user; no agent may call the
// routes.
func TestBackupRoutesAreUserOnly(t *testing.T) {
	s := newTestServer(t)
	sess := liveSession(t, s, api.RoleDeveloper)
	for _, r := range backupRoutes {
		if !s.registeredPattern(r.pattern) {
			t.Fatalf("%s is not registered", r.pattern)
		}
		for _, row := range s.routeTable {
			if row.pattern == r.pattern && row.access != userOnly {
				t.Errorf("%s has access %s", r.pattern, row.access)
			}
		}
		method, path, _ := strings.Cut(r.pattern, " ")
		var rec *httptest.ResponseRecorder
		if path == "/api/settings/import" {
			rec = rawSocket(t, s, path, []byte("PK"), sess.ID)
		} else {
			rec = socketDo(t, s, method, path, r.body, sess.ID)
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("an agent got %d from %s", rec.Code, r.pattern)
		}
	}
}

// rawSocket posts body as application/octet-stream through the socket.
func rawSocket(t *testing.T, s *Server, path string, body []byte, session string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest("POST", "http://omo"+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if session != "" {
		req.Header.Set(SessionHeader, session)
	}
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	return rec
}

func zipNames(t *testing.T, data []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}

func TestExportRoute(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	resp := b.do("GET", "/api/settings/export", nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content type %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "omo-settings-") || !strings.HasSuffix(cd, `.zip"`) && !strings.HasSuffix(cd, ".zip") {
		t.Fatalf("disposition %q", cd)
	}
	data, _ := io.ReadAll(resp.Body)
	names := zipNames(t, data)
	if !slices.Contains(names, "manifest.json") || !slices.Contains(names, "settings.yaml") || slices.Contains(names, "omo.db") {
		t.Fatalf("names %q", names)
	}
	resp = b.do("GET", "/api/settings/export?database=true", nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	data, _ = io.ReadAll(resp.Body)
	if !slices.Contains(zipNames(t, data), "omo.db") {
		t.Fatal("no database in the zip")
	}
	wantStatus(t, b.do("GET", "/api/settings/export?database=yes", nil, bearer(s)).StatusCode, http.StatusBadRequest)
}

// TestImportRoutes stages a zip, reads the plan and applies it: the setting
// changes and the change is announced as the app's own.
func TestImportRoutes(t *testing.T) {
	a := newTestServer(t)
	g := a.settings.General()
	g.Limits.Tasks = 7
	if err := a.settings.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := a.home().Export(context.Background(), &buf, false, time.Now()); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t)
	rec := rawSocket(t, s, "/api/settings/import", []byte("not a zip"), "")
	wantStatus(t, rec.Code, http.StatusUnprocessableEntity)
	rec = rawSocket(t, s, "/api/settings/import", buf.Bytes(), "")
	wantStatus(t, rec.Code, http.StatusOK)
	plan := decodeBody[api.ImportPlan](t, rec.Body)
	found := false
	for _, f := range plan.Files {
		if f.Path == "settings.yaml" && f.Change == api.ImportReplace {
			found = true
		}
	}
	if !found || plan.Database || plan.AgentsRunning != 0 {
		t.Fatalf("plan %+v", plan)
	}
	start := lastEventID(t, s)
	rec = socketDo(t, s, "POST", "/api/settings/import/apply", api.ImportApply{ID: plan.ID}, "")
	wantStatus(t, rec.Code, http.StatusOK)
	res := decodeBody[api.ImportResult](t, rec.Body)
	if !slices.Contains(res.Written, "settings.yaml") || res.Backup == "" {
		t.Fatalf("result %+v", res)
	}
	if s.settings.General().Limits.Tasks != 7 {
		t.Fatal("the setting did not change")
	}
	ev := wantEvents(t, s, api.KindSettingsChanged, start, 1)
	var d api.SettingsChangedData
	if err := json.Unmarshal(ev[0], &d); err != nil || d.Source != api.SourceApp || d.Section != api.SectionGeneral {
		t.Fatalf("event %s", ev[0])
	}
	rec = socketDo(t, s, "POST", "/api/settings/import/apply", api.ImportApply{ID: plan.ID}, "")
	wantStatus(t, rec.Code, http.StatusNotFound)
}

// TestDatabaseImportRefusedWhileAgentsRun: with an agent running the
// database is refused and nothing changes; the settings alone may go in.
func TestDatabaseImportRefusedWhileAgentsRun(t *testing.T) {
	s := newTestServer(t)
	var buf bytes.Buffer
	if err := s.home().Export(context.Background(), &buf, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	liveSession(t, s, api.RoleDeveloper)
	rec := rawSocket(t, s, "/api/settings/import", buf.Bytes(), "")
	wantStatus(t, rec.Code, http.StatusOK)
	plan := decodeBody[api.ImportPlan](t, rec.Body)
	if !plan.Database || plan.AgentsRunning != 1 {
		t.Fatalf("plan %+v", plan)
	}
	rec = socketDo(t, s, "POST", "/api/settings/import/apply", api.ImportApply{ID: plan.ID, Database: true}, "")
	wantStatus(t, rec.Code, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "1 agent runs") {
		t.Fatalf("answer %s", rec.Body)
	}
	if _, err := os.Stat(filepath.Join(s.opts.Layout.Root, homezip.PendingDatabase)); !os.IsNotExist(err) {
		t.Fatal("a database waits after the refusal")
	}
	if paused, _ := s.board.Paused(context.Background()); paused {
		t.Fatal("paused after the refusal")
	}
	rec = socketDo(t, s, "POST", "/api/settings/import/apply", api.ImportApply{ID: plan.ID}, "")
	wantStatus(t, rec.Code, http.StatusOK)
}

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

// TestDatabaseImportPausesBeforeItChangesAnything: the board is paused
// before the check that no agent runs, so nothing can start in between;
// an import that fails afterwards takes the pause back, and a pause the
// user set stays.
func TestDatabaseImportPausesBeforeItChangesAnything(t *testing.T) {
	a := newTestServer(t)
	if err := a.settings.PutPromptIf("", "developer-instructions", "From the zip."); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := a.home().Export(context.Background(), &buf, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t)
	if err := s.settings.PutPromptIf("", "developer-instructions", "Here."); err != nil {
		t.Fatal(err)
	}
	rec := rawSocket(t, s, "/api/settings/import", buf.Bytes(), "")
	wantStatus(t, rec.Code, http.StatusOK)
	plan := decodeBody[api.ImportPlan](t, rec.Body)

	// The engine's view while the import checks for agents: paused.
	pausedAtCheck := false
	beforeImportCheck = func() { pausedAtCheck, _ = s.board.Paused(context.Background()) }
	t.Cleanup(func() { beforeImportCheck = nil })

	// A folder in place of the prompt file: the apply fails after the check.
	prompt := filepath.Join(s.opts.Layout.Root, "prompts", "developer-instructions.md")
	if err := os.Remove(prompt); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(prompt, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	rec = socketDo(t, s, "POST", "/api/settings/import/apply", api.ImportApply{ID: plan.ID, Database: true}, "")
	if rec.Code == http.StatusOK {
		t.Fatal("the import succeeded over a folder")
	}
	if !pausedAtCheck {
		t.Fatal("the board was not paused when the import checked for agents")
	}
	if paused, _ := s.board.Paused(context.Background()); paused {
		t.Fatal("a failed import left the board paused")
	}

	// A pause of the user's stays after a failure.
	if err := s.board.SetPaused(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	rec = socketDo(t, s, "POST", "/api/settings/import/apply", api.ImportApply{ID: plan.ID, Database: true}, "")
	if rec.Code == http.StatusOK {
		t.Fatal("the import succeeded over a folder")
	}
	if paused, _ := s.board.Paused(context.Background()); !paused {
		t.Fatal("a failed import took back the user's pause")
	}
	if err := s.board.SetPaused(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(prompt); err != nil {
		t.Fatal(err)
	}
	rec = socketDo(t, s, "POST", "/api/settings/import/apply", api.ImportApply{ID: plan.ID, Database: true}, "")
	wantStatus(t, rec.Code, http.StatusOK)
	if paused, _ := s.board.Paused(context.Background()); !paused {
		t.Fatal("the board is not paused until the restart")
	}
}
