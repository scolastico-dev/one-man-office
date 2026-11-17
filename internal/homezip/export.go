package homezip

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// Home is the omo home an export reads and an import writes, with the
// settings store that owns its settings files.
type Home struct {
	Layout   globalhome.Layout
	Settings *settings.Store
	// Version is this omo's version (written to and checked against the
	// manifest).
	Version string
}

// FileName is the name of a settings zip made at t.
func FileName(t time.Time) string {
	return "omo-settings-" + t.Local().Format("2006-01-02") + ".zip"
}

// limit is the largest size of a settings file at rel; false for a path
// that is no settings file.
func (h Home) limit(rel string) (int64, bool) {
	e, ok := Allowed(rel)
	if !ok {
		return 0, false
	}
	if e.Server {
		return homefile.MaxConfigFile, true
	}
	return h.Settings.ItemFile(rel)
}

// files lists the allowlisted files present in the home, sorted. Settings
// files the store cannot load are left out: they are not in effect.
func (h Home) files() ([]string, error) {
	broken := map[string]bool{}
	for _, e := range h.Settings.Errors() {
		if rel, err := filepath.Rel(h.Layout.Root, e.File); err == nil {
			broken[filepath.ToSlash(rel)] = true
		}
	}
	var out []string
	add := func(rel string) {
		if _, ok := h.limit(rel); ok && !broken[rel] {
			out = append(out, rel)
		}
	}
	for _, e := range Exported {
		dir, _, folder := strings.Cut(e.Pattern, "/*")
		if !folder {
			if info, err := os.Lstat(filepath.Join(h.Layout.Root, e.Pattern)); err == nil && info.Mode().IsRegular() {
				add(e.Pattern)
			}
			continue
		}
		entries, err := os.ReadDir(filepath.Join(h.Layout.Root, dir))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, d := range entries {
			rel := path.Join(dir, d.Name())
			if d.Type().IsRegular() && matches(e.Pattern, rel) {
				add(rel)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Export writes a settings zip to w: manifest.json, every allowlisted file
// of the home and, with database, a consistent snapshot of the database as
// omo.db. The snapshot is made in the home's run folder and removed again.
func (h Home) Export(ctx context.Context, w io.Writer, database bool, now time.Time) error {
	rels, err := h.files()
	if err != nil {
		return err
	}
	var snapshot string
	if database {
		f, err := os.CreateTemp(h.Layout.Run, "export-*.db")
		if err != nil {
			return err
		}
		snapshot = f.Name()
		f.Close()
		// VACUUM INTO writes only into a file that does not exist.
		os.Remove(snapshot)
		defer os.Remove(snapshot)
		if err := snapshotDatabase(ctx, h.Layout.DB, snapshot); err != nil {
			return err
		}
	}
	zw := zip.NewWriter(w)
	m := api.BackupManifest{Format: Format, OmoVersion: h.Version, SchemaVersion: db.SchemaVersion, CreatedAt: now.UTC().Truncate(time.Second), Database: database}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeEntry(zw, ManifestName, append(raw, '\n'), now); err != nil {
		return err
	}
	for _, rel := range rels {
		max, _ := h.limit(rel)
		data, err := homefile.Read(filepath.Join(h.Layout.Root, filepath.FromSlash(rel)), max)
		if os.IsNotExist(err) {
			// Removed since the listing.
			continue
		}
		if err != nil {
			return err
		}
		if err := writeEntry(zw, rel, data, now); err != nil {
			return err
		}
	}
	if snapshot != "" {
		f, err := homefile.Open(snapshot)
		if err != nil {
			return err
		}
		defer f.Close()
		hdr := &zip.FileHeader{Name: DatabaseName, Method: zip.Deflate, Modified: now}
		hdr.SetMode(0o600)
		ew, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if _, err := io.Copy(ew, f); err != nil {
			return err
		}
	}
	return zw.Close()
}

func writeEntry(zw *zip.Writer, name string, data []byte, now time.Time) error {
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now}
	hdr.SetMode(0o600)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
