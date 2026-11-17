package homezip

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/usage"
)

// testHome is a fresh omo home with the settings store as the server opens
// it and an open database.
type testHome struct {
	Home
	db      *sql.DB
	changes chan settings.Change
}

func newHome(t *testing.T) *testHome {
	t.Helper()
	t.Setenv("OMO_HOME", t.TempDir())
	l, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	h := &testHome{changes: make(chan settings.Change, 256)}
	st, err := settings.Open(l.Root, settings.Options{
		PromptDefaults:      prompts.Defaults(),
		UsageDefaults:       usage.Defaults(),
		ValidatePrompt:      prompts.Check,
		ValidateUsageScript: usage.Check,
		OnChange: func(c settings.Change) {
			select {
			case h.changes <- c:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(l.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	h.Home = Home{Layout: l, Settings: st, Version: "v1.2.0"}
	h.db = d
	return h
}

// fill writes one settings file of every kind through the store, and
// server.yaml.
func (h *testHome) fill(t *testing.T) {
	t.Helper()
	st := h.Settings
	g := st.General()
	g.AgentEnv = map[string]string{"GIT_AUTHOR_NAME": "Someone", "TOKEN": "secret-value"}
	g.Limits.Tasks = 3
	if err := st.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	if err := st.PutProfileIf("", settings.Profile{Name: "work", CLI: api.CLICustom, Command: "agent", Env: map[string]string{"KEY": "v"},
		Models: map[string]settings.Model{"a": {}, "b": {}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRoleIf("", settings.Role{Name: "developer", Models: []string{"work/a"}, Assignment: api.AssignmentRoundRobin}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutPromptIf("", "developer-instructions", "Write tests first."); err != nil {
		t.Fatal(err)
	}
	if err := st.PutUsageScriptIf("0", "mine", "function usage() return {} end"); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSkillSetIf("0", settings.SkillSet{Name: "tools", Repository: "https://example.invalid/tools.git", Update: api.SkillSetPinned, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutPluginIf("0", settings.PluginSettings{Name: "notify", Source: "https://example.invalid/notify.git", Config: map[string]any{"user_key": "k"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutMCPServerIf("0", settings.MCPServer{Name: "docs", Enabled: true, Transport: api.MCPTransportHTTP, URL: "https://mcp.example.invalid/mcp",
		Headers: map[string]string{"Authorization": "Bearer mcp-secret-value"}, Global: true, Roles: []string{"developer"}}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProject(repo, "app"); err != nil {
		t.Fatal(err)
	}
	if _, err := serverconfig.Set(h.Layout.ServerConfig, "port", "9123"); err != nil {
		t.Fatal(err)
	}
}

func (h *testHome) export(t *testing.T, database bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := h.Export(context.Background(), &buf, database, time.Now()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (h *testHome) stage(t *testing.T, zipped []byte) api.ImportPlan {
	t.Helper()
	plan, err := h.Stage(context.Background(), bytes.NewReader(zipped))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func entries(t *testing.T, zipped []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		data, err := readEntry(f, 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = data
	}
	return out
}

// TestAllowlistMatchesSettingsStore fails when the settings store reads a
// file the allowlist does not export, or the allowlist names a settings file
// the store does not know: a new kind of settings file must be added here.
func TestAllowlistMatchesSettingsStore(t *testing.T) {
	h := newHome(t)
	var listed []string
	for _, e := range Exported {
		if !e.Server {
			listed = append(listed, e.Pattern)
		}
	}
	slices.Sort(listed)
	if got := h.Settings.Patterns(); !slices.Equal(got, listed) {
		t.Fatalf("the settings store reads %q, the allowlist exports %q", got, listed)
	}
}

// TestLayoutClassified fails when the home layout gains a file or folder
// that is in neither the allowlist nor NotExported.
func TestLayoutClassified(t *testing.T) {
	h := newHome(t)
	v := reflect.ValueOf(h.Layout)
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if name == "Root" {
			continue
		}
		rel, err := filepath.Rel(h.Layout.Root, v.Field(i).String())
		if err != nil || strings.Contains(rel, string(filepath.Separator)) {
			t.Fatalf("Layout.%s is not at the top of the home", name)
		}
		file := map[string]bool{"Settings": true, "ServerConfig": true, "Token": true, "DB": true}[name]
		if err := Classified(rel, !file); err != nil {
			t.Errorf("Layout.%s: %v", name, err)
		}
	}
}

func TestExportHoldsAllowlistedFilesAndManifest(t *testing.T) {
	h := newHome(t)
	h.fill(t)
	// Things that are not settings.
	os.WriteFile(filepath.Join(h.Layout.Logs, "agent.log"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(h.Layout.Plugins, ".omo-write-123"), []byte("x"), 0o600)
	os.MkdirAll(filepath.Join(h.Layout.Plugins, "notify"), 0o700)
	os.WriteFile(filepath.Join(h.Layout.Plugins, "notify", "plugin.yaml"), []byte("x"), 0o600)
	got := entries(t, h.export(t, false))
	var names []string
	for n := range got {
		names = append(names, n)
	}
	slices.Sort(names)
	want := []string{"manifest.json", "mcpservers/docs.yaml", "plugins/notify.yaml", "profiles/work.yaml", "projects/app.yaml", "prompts/developer-instructions.md",
		"roles/ceo.yaml", "roles/developer.yaml", "roles/pm.yaml", "roles/reviewer.yaml",
		"server.yaml", "settings.yaml", "skillsets/tools.yaml", "usage/mine.lua"}
	if !slices.Equal(names, want) {
		t.Fatalf("entries %q, want %q", names, want)
	}
	var m api.BackupManifest
	if err := json.Unmarshal(got[ManifestName], &m); err != nil {
		t.Fatal(err)
	}
	if m.Format != Format || m.OmoVersion != "v1.2.0" || m.SchemaVersion != db.SchemaVersion || m.Database || m.CreatedAt.IsZero() {
		t.Fatalf("manifest %+v", m)
	}
	if !bytes.Contains(got["settings.yaml"], []byte("secret-value")) {
		t.Fatal("the environment values are not in the zip")
	}
	if !bytes.Contains(got["mcpservers/docs.yaml"], []byte("mcp-secret-value")) {
		t.Fatal("the MCP server's header values are not in the zip")
	}
}

// TestRoundTrip exports one home and imports the zip into a fresh one: the
// settings are equal afterwards, server.yaml only when asked.
func TestRoundTrip(t *testing.T) {
	a := newHome(t)
	a.fill(t)
	zipped := a.export(t, false)

	b := newHome(t)
	plan := b.stage(t, zipped)
	if plan.Database || plan.Server != api.ImportAdd || len(plan.Files) == 0 {
		t.Fatalf("plan %+v", plan)
	}
	changes := map[string]string{}
	for _, f := range plan.Files {
		changes[f.Path] = f.Change
	}
	if changes["profiles/work.yaml"] != api.ImportAdd || changes["settings.yaml"] != api.ImportReplace || changes["roles/ceo.yaml"] != api.ImportSame {
		t.Fatalf("changes %v", changes)
	}
	res, err := b.Apply(context.Background(), plan.ID, ApplyOptions{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.RestartNeeded || res.DatabasePending || res.Backup == "" || slices.Contains(res.Written, "server.yaml") {
		t.Fatalf("result %+v", res)
	}
	// The replaced settings.yaml is kept.
	if _, err := os.Stat(filepath.Join(res.Backup, "settings.yaml")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Settings.General(), b.Settings.General()) {
		t.Fatalf("general settings differ:\n%+v\n%+v", a.Settings.General(), b.Settings.General())
	}
	for _, check := range []func(st *settings.Store) any{
		func(st *settings.Store) any { return st.Profiles() },
		func(st *settings.Store) any { return st.Roles() },
		func(st *settings.Store) any { return st.Prompts() },
		func(st *settings.Store) any { return st.SkillSets() },
		func(st *settings.Store) any { return st.Plugins() },
		func(st *settings.Store) any { return st.MCPServers() },
		func(st *settings.Store) any { return st.UsageScripts() },
		func(st *settings.Store) any { return st.Projects(true) },
	} {
		if x, y := check(a.Settings), check(b.Settings); !reflect.DeepEqual(x, y) {
			t.Fatalf("settings differ:\n%+v\n%+v", x, y)
		}
	}
	// The store reported the changes as its own, not as hand edits.
	found := false
	for len(b.changes) > 0 {
		c := <-b.changes
		if c.FromDisk {
			t.Fatalf("change %+v reported as a hand edit", c)
		}
		if c.Section == settings.SectionProfiles && c.Name == "work" {
			found = true
		}
	}
	if !found {
		t.Fatal("no change for the imported profile")
	}
	if c, _ := serverconfig.Load(b.Layout.ServerConfig); c.Port == 9123 {
		t.Fatal("server.yaml was applied without being asked")
	}
	// The waiting zip is gone.
	if _, err := b.Apply(context.Background(), plan.ID, ApplyOptions{Now: time.Now()}); !errors.Is(err, ErrNoUpload) {
		t.Fatalf("second apply: %v", err)
	}

	// With the server option, server.yaml is written too.
	plan = b.stage(t, zipped)
	res, err = b.Apply(context.Background(), plan.ID, ApplyOptions{Server: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.RestartNeeded || !slices.Equal(res.Written, []string{"server.yaml"}) {
		t.Fatalf("result %+v", res)
	}
	if c, _ := serverconfig.Load(b.Layout.ServerConfig); c.Port != 9123 {
		t.Fatalf("port %d", c.Port)
	}
}

// TestImportLeavesOtherFilesAlone: a file of the allowlist the zip does not
// hold stays.
func TestImportLeavesOtherFilesAlone(t *testing.T) {
	a := newHome(t)
	zipped := a.export(t, false)
	b := newHome(t)
	b.fill(t)
	res, err := b.Apply(context.Background(), b.stage(t, zipped).ID, ApplyOptions{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Settings.Profile("work"); !ok {
		t.Fatal("a profile the zip does not hold was removed")
	}
	if b.Settings.General().Limits.Tasks == 3 {
		t.Fatalf("settings.yaml was not replaced: %+v", res)
	}
}

// TestFailedRenamePutsEverythingBack: when a rename fails half-way, the
// files renamed before are restored and nothing new stays.
func TestFailedRenamePutsEverythingBack(t *testing.T) {
	a := newHome(t)
	a.fill(t)
	g := a.Settings.General()
	g.Limits.Tasks = 7
	if err := a.Settings.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	if err := a.Settings.PutPromptIf("", "developer-instructions", "Other text."); err != nil {
		t.Fatal(err)
	}
	if err := a.Settings.PutPromptIf("", "pm-instructions", "New text."); err != nil {
		t.Fatal(err)
	}
	zipped := a.export(t, false)
	b := newHome(t)
	b.fill(t)
	before := snapshotFiles(t, b.Layout.Root)
	calls := 0
	renameFile = func(from, to string) error {
		calls++
		if calls == 2 {
			return errors.New("disk full")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { renameFile = os.Rename })
	plan := b.stage(t, zipped)
	if _, err := b.Apply(context.Background(), plan.ID, ApplyOptions{Server: true, Now: time.Now()}); err == nil {
		t.Fatal("apply succeeded")
	}
	after := snapshotFiles(t, b.Layout.Root)
	for rel, data := range before {
		if !bytes.Equal(after[rel], data) {
			t.Errorf("%s changed", rel)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Errorf("%s was left behind", rel)
		}
	}
	if b.Settings.General().Limits.Tasks != 3 {
		t.Fatal("the store holds the zip's settings")
	}
}

// snapshotFiles reads every allowlisted file of the home.
func snapshotFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if _, ok := Allowed(rel); ok || strings.HasPrefix(filepath.Base(rel), ".omo-") {
			data, _ := os.ReadFile(p)
			out[rel] = data
		}
		return nil
	})
	return out
}

// makeZip builds a zip; a file with a nil body becomes a folder entry, a
// name starting with "link:" a symbolic link.
func makeZip(t *testing.T, manifest *api.BackupManifest, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if manifest != nil {
		raw, _ := json.Marshal(manifest)
		w, _ := zw.Create(ManifestName)
		w.Write(raw)
	}
	for name, body := range files {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(0o600)
		if target, ok := strings.CutPrefix(name, "link:"); ok {
			hdr.Name = target
			hdr.SetMode(fs.ModeSymlink | 0o777)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func goodManifest() *api.BackupManifest {
	return &api.BackupManifest{Format: Format, OmoVersion: "v1.2.0", SchemaVersion: db.SchemaVersion, CreatedAt: time.Now()}
}

func TestHostileZipsAreRefused(t *testing.T) {
	newer := goodManifest()
	newer.SchemaVersion = db.SchemaVersion + 1
	newerFormat := goodManifest()
	newerFormat.Format = Format + 1
	newerOmo := goodManifest()
	newerOmo.OmoVersion = "v1.3.0"
	withDB := goodManifest()
	withDB.Database = true
	big := strings.Repeat("#", 1<<20+1)
	cases := []struct {
		name     string
		manifest *api.BackupManifest
		files    map[string]string
		want     string
	}{
		{"no manifest", nil, map[string]string{"settings.yaml": ""}, "no manifest.json"},
		{"broken manifest", nil, map[string]string{"manifest.json": "{"}, "cannot be read"},
		{"zip slip", goodManifest(), map[string]string{"../settings.yaml": ""}, "outside omo's home"},
		{"zip slip inside", goodManifest(), map[string]string{"profiles/../../x.yaml": ""}, "outside omo's home"},
		{"absolute", goodManifest(), map[string]string{"/etc/settings.yaml": ""}, "outside omo's home"},
		{"backslash", goodManifest(), map[string]string{`profiles\a.yaml`: ""}, "outside omo's home"},
		{"drive", goodManifest(), map[string]string{"C:/settings.yaml": ""}, "outside omo's home"},
		{"link", goodManifest(), map[string]string{"link:profiles/a.yaml": "/etc/passwd"}, "link"},
		{"unknown path", goodManifest(), map[string]string{"notes.txt": "hi"}, "not one of omo's settings files"},
		{"token", goodManifest(), map[string]string{"token": "x"}, "not one of omo's settings files"},
		{"plugin tree", goodManifest(), map[string]string{"plugins/notify/main.lua": "x"}, "not one of omo's settings files"},
		{"unknown folder", goodManifest(), map[string]string{"logs/": ""}, "not part of omo's settings"},
		{"oversize", goodManifest(), map[string]string{"settings.yaml": big}, "larger than"},
		{"broken yaml", goodManifest(), map[string]string{"profiles/a.yaml": "cli: [\n"}, "profiles/a.yaml"},
		{"invalid value", goodManifest(), map[string]string{"settings.yaml": "limits:\n  tasks: -4\n"}, "settings.yaml"},
		{"bad server.yaml", goodManifest(), map[string]string{"server.yaml": "port: 99999999\n"}, "server.yaml"},
		{"newer schema", newer, nil, "newer omo"},
		{"newer format", newerFormat, nil, "newer omo"},
		{"newer omo", newerOmo, nil, "newer omo"},
		{"database flag without database", withDB, nil, "disagree"},
		{"database without flag", goodManifest(), map[string]string{"omo.db": "x"}, "disagree"},
		{"damaged database", withDB, map[string]string{"omo.db": "not sqlite at all"}, "damaged"},
		{"twice", goodManifest(), map[string]string{"plugins/a.yaml": "source: https://example.invalid/a.git\n", "plugins/A.yaml": "source: https://example.invalid/a.git\n"}, "twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHome(t)
			before := snapshotFiles(t, h.Layout.Root)
			_, err := h.Stage(context.Background(), bytes.NewReader(makeZip(t, c.manifest, c.files)))
			var p *Problem
			if !errors.As(err, &p) {
				t.Fatalf("err = %v, want a problem", err)
			}
			if !strings.Contains(p.Message, c.want) {
				t.Fatalf("message %q, want %q", p.Message, c.want)
			}
			if after := snapshotFiles(t, h.Layout.Root); !reflect.DeepEqual(before, after) {
				t.Fatal("the home changed")
			}
			if left, _ := os.ReadDir(h.staging()); len(left) != 0 {
				t.Fatalf("staging keeps %d entries", len(left))
			}
		})
	}
}

func TestTooManyEntriesAndTooLargeArchive(t *testing.T) {
	h := newHome(t)
	old, oldArchive := MaxEntries, MaxArchive
	t.Cleanup(func() { MaxEntries, MaxArchive = old, oldArchive })
	MaxEntries = 3
	files := map[string]string{"prompts/a.md": "", "prompts/b.md": "", "prompts/c.md": ""}
	if _, err := h.Stage(context.Background(), bytes.NewReader(makeZip(t, goodManifest(), files))); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("err = %v", err)
	}
	MaxEntries = old
	MaxArchive = 100
	if _, err := h.Stage(context.Background(), bytes.NewReader(makeZip(t, goodManifest(), files))); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}

// TestFolderEntriesOfZipToolsAreAccepted: a zip made again with a zip tool
// has entries for its folders.
func TestFolderEntriesOfZipToolsAreAccepted(t *testing.T) {
	h := newHome(t)
	plan, err := h.Stage(context.Background(), bytes.NewReader(makeZip(t, goodManifest(), map[string]string{"prompts/": "", "prompts/developer-instructions.md": "Hi."})))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != "prompts/developer-instructions.md" {
		t.Fatalf("plan %+v", plan)
	}
}

// TestDatabaseSnapshotWhileWriting exports while another connection keeps
// writing in transactions that each change two tables: every snapshot is
// intact and holds whole transactions only.
func TestDatabaseSnapshotWhileWriting(t *testing.T) {
	h := newHome(t)
	if _, err := h.db.Exec(`CREATE TABLE a (n INTEGER); CREATE TABLE b (n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		for i := 0; ; i++ {
			if ctx.Err() != nil {
				done <- nil
				return
			}
			tx, err := h.db.Begin()
			if err != nil {
				done <- err
				return
			}
			tx.Exec(`INSERT INTO a VALUES (?)`, i)
			tx.Exec(`INSERT INTO b VALUES (?)`, i)
			if err := tx.Commit(); err != nil {
				done <- err
				return
			}
		}
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}()
	time.Sleep(50 * time.Millisecond)
	for round := 0; round < 3; round++ {
		got := entries(t, h.export(t, true))
		var m api.BackupManifest
		json.Unmarshal(got[ManifestName], &m)
		if !m.Database {
			t.Fatal("manifest says no database")
		}
		p := filepath.Join(t.TempDir(), "snap.db")
		os.WriteFile(p, got[DatabaseName], 0o600)
		if err := checkDatabase(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		snap, err := sql.Open("sqlite", "file:"+p+"?mode=ro&immutable=1")
		if err != nil {
			t.Fatal(err)
		}
		var na, nb int
		snap.QueryRow(`SELECT count(*) FROM a`).Scan(&na)
		snap.QueryRow(`SELECT count(*) FROM b`).Scan(&nb)
		snap.Close()
		if na != nb {
			t.Fatalf("snapshot holds %d rows of a and %d of b", na, nb)
		}
		if na == 0 {
			t.Fatal("snapshot holds no rows")
		}
	}
	if left, _ := filepath.Glob(filepath.Join(h.Layout.Run, "export-*")); len(left) != 0 {
		t.Fatalf("snapshot files left: %v", left)
	}
}

// TestDatabaseImportWaitsForTheNextStart: the zip's database goes next to
// omo.db and replaces it at the next start, the old one kept.
func TestDatabaseImportWaitsForTheNextStart(t *testing.T) {
	a := newHome(t)
	if _, err := a.db.Exec(`INSERT INTO server_state (key, value) VALUES ('marker', 'from a')`); err != nil {
		t.Fatal(err)
	}
	zipped := a.export(t, true)
	b := newHome(t)
	plan := b.stage(t, zipped)
	if !plan.Database {
		t.Fatal("plan without database")
	}
	refused := errors.New("agents run")
	if _, err := b.Apply(context.Background(), plan.ID, ApplyOptions{Database: true, Now: time.Now(), Ready: func() error { return refused }}); !errors.Is(err, refused) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.Layout.Root, PendingDatabase)); !os.IsNotExist(err) {
		t.Fatal("a refused import left a database")
	}
	res, err := b.Apply(context.Background(), plan.ID, ApplyOptions{Database: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DatabasePending || !res.RestartNeeded {
		t.Fatalf("result %+v", res)
	}
	b.db.Close()
	moved, err := SwapPendingDatabase(b.Layout, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "omo.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.Layout.Root, PendingDatabase)); !os.IsNotExist(err) {
		t.Fatal("the pending database is still there")
	}
	d, err := db.Open(b.Layout.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var v string
	if err := d.QueryRow(`SELECT value FROM server_state WHERE key = 'marker'`).Scan(&v); err != nil || v != "from a" {
		t.Fatalf("marker %q %v", v, err)
	}
	if again, err := SwapPendingDatabase(b.Layout, time.Now()); err != nil || again != "" {
		t.Fatalf("second swap %q %v", again, err)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v1.3.0", "v1.2.9", true}, {"v1.2.0", "v1.2.0", false}, {"v1.2.0", "v1.10.0", false},
		{"v2.0.0-rc1", "v1.9.9", true}, {"dev", "v1.0.0", false}, {"v1.0.0", "dev", false}, {"v1.0.1-3-gabc", "v1.0.0", true},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestUnclassifiedNamesNewEntries(t *testing.T) {
	h := newHome(t)
	h.fill(t)
	os.MkdirAll(filepath.Join(h.Layout.Plugins, "notify", "lib"), 0o700)
	os.WriteFile(filepath.Join(h.Layout.Plugins, ".cache"), nil, 0o600)
	if left, err := Unclassified(h.Layout.Root); err != nil || len(left) != 0 {
		t.Fatalf("fresh home: %q %v", left, err)
	}
	os.WriteFile(filepath.Join(h.Layout.Root, "notes.yaml"), nil, 0o600)
	os.MkdirAll(filepath.Join(h.Layout.Root, "cache"), 0o700)
	os.WriteFile(filepath.Join(h.Layout.Profiles, "readme.txt"), nil, 0o600)
	os.MkdirAll(filepath.Join(h.Layout.Profiles, "old"), 0o700)
	left, err := Unclassified(h.Layout.Root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"cache", "notes.yaml", "profiles/old", "profiles/readme.txt"}; !slices.Equal(left, want) {
		t.Fatalf("unclassified %q, want %q", left, want)
	}
}

// A settings file with a broken hand edit keeps its last valid content in
// effect; an export that left it out would lose it, so the export is
// refused and names the file.
func TestExportRefusesBrokenSettingsFiles(t *testing.T) {
	h := newHome(t)
	h.fill(t)
	path := filepath.Join(h.Layout.Root, "profiles", "work.yaml")
	if err := os.WriteFile(path, []byte("cli: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Settings.Reload()
	if len(h.Settings.Errors()) == 0 {
		t.Fatal("the broken file is not reported")
	}
	var buf bytes.Buffer
	err := h.Export(context.Background(), &buf, false, time.Now())
	var p *Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, "profiles/work.yaml") {
		t.Fatalf("export: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatal("the refused export wrote a zip")
	}
}

// An apply that fails after the database was placed leaves the waiting
// zip as it was: a second apply, once the cause is gone, imports it with
// its database.
func TestAFailedApplyKeepsTheDatabaseForTheNextTry(t *testing.T) {
	a := newHome(t)
	a.fill(t)
	zipped := a.export(t, true)
	b := newHome(t)
	plan := b.stage(t, zipped)
	// A folder where the zip has a profile: the copy of the replaced
	// files fails.
	blocker := filepath.Join(b.Layout.Root, "profiles", "work.yaml")
	if err := os.MkdirAll(filepath.Join(blocker, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Apply(context.Background(), plan.ID, ApplyOptions{Database: true, Now: time.Now()}); err == nil {
		t.Fatal("the apply succeeded over a folder")
	}
	if _, err := os.Stat(filepath.Join(b.Layout.Root, PendingDatabase)); !os.IsNotExist(err) {
		t.Fatal("a failed import left a database waiting")
	}
	if err := os.RemoveAll(blocker); err != nil {
		t.Fatal(err)
	}
	res, err := b.Apply(context.Background(), plan.ID, ApplyOptions{Database: true, Now: time.Now()})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if !res.DatabasePending {
		t.Fatalf("result %+v", res)
	}
}
