package homezip

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

// ErrNoUpload is returned by Apply when no checked zip waits under the id
// (it was replaced by a newer upload, applied already or omo restarted).
var ErrNoUpload = errors.New("the uploaded zip is no longer there; choose the file again")

// staging is the folder of the uploaded zip that waits for Apply.
func (h Home) staging() string { return filepath.Join(h.Layout.Run, "import") }

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Stage reads an uploaded zip from r (at most MaxArchive bytes) into the
// run folder and checks it completely, changing nothing of the home: the
// structure (readArchive), every settings file the way the settings store
// would load it next to the current ones, server.yaml as the start options
// are read, and the database (checkDatabase). A zip that passes waits under
// the plan's ID for Apply; a later Stage replaces it. Problems are returned
// as *Problem.
func (h Home) Stage(ctx context.Context, r io.Reader) (api.ImportPlan, error) {
	if err := os.RemoveAll(h.staging()); err != nil {
		return api.ImportPlan{}, err
	}
	id := newID()
	dir := filepath.Join(h.staging(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return api.ImportPlan{}, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	upload := filepath.Join(dir, "upload.zip")
	f, err := os.OpenFile(upload, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return api.ImportPlan{}, err
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxArchive+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return api.ImportPlan{}, fmt.Errorf("receive the zip: %w", err)
	}
	if n > MaxArchive {
		return api.ImportPlan{}, problem("the file is larger than %s", homefile.Size(MaxArchive))
	}
	a, err := h.check(ctx, dir, true)
	if err != nil {
		return api.ImportPlan{}, err
	}
	plan := api.ImportPlan{ID: id, Manifest: a.manifest, Files: []api.ImportFile{}, Database: a.database != nil}
	for _, rel := range sortedKeys(a.files) {
		change := h.change(rel, a.files[rel])
		if e, _ := Allowed(rel); e.Server {
			plan.Server = change
			continue
		}
		plan.Files = append(plan.Files, api.ImportFile{Path: rel, Change: change})
	}
	ok = true
	return plan, nil
}

// check reads the waiting zip in dir and checks it; with extract, the
// zip's database is unpacked next to it and checked (Apply uses that copy).
func (h Home) check(ctx context.Context, dir string, extract bool) (*archive, error) {
	f, err := homefile.Open(filepath.Join(dir, "upload.zip"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	a, err := readArchive(f, info.Size(), h.Version, h.limit)
	if err != nil {
		return nil, err
	}
	scratch := filepath.Join(dir, "check")
	defer os.RemoveAll(scratch)
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return nil, err
	}
	store := map[string][]byte{}
	for rel, data := range a.files {
		if e, _ := Allowed(rel); e.Server {
			if err := checkServerConfig(scratch, data); err != nil {
				return nil, err
			}
			continue
		}
		store[rel] = data
	}
	if errs := h.Settings.CheckFiles(filepath.Join(scratch, "home"), store); len(errs) > 0 {
		lines := make([]string, 0, len(errs))
		for i, e := range errs {
			if i == 5 {
				lines = append(lines, fmt.Sprintf("and %d more", len(errs)-5))
				break
			}
			lines = append(lines, fmt.Sprintf("%s: %s", e.File, e.Message))
		}
		return nil, problem("the zip's settings do not load: %s", strings.Join(lines, "; "))
	}
	if extract && a.database != nil {
		p := filepath.Join(dir, DatabaseName)
		if err := extractEntry(a, p); err != nil {
			return nil, err
		}
		if err := checkDatabase(ctx, p); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// extractEntry writes the zip's database to p.
func extractEntry(a *archive, p string) error {
	rc, err := a.database.Open()
	if err != nil {
		return problem("the database cannot be read from the zip")
	}
	defer rc.Close()
	out, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(rc, MaxDatabase+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return problem("the database cannot be read from the zip")
	}
	if n > MaxDatabase {
		return problem("the database in the zip is larger than %s", homefile.Size(MaxDatabase))
	}
	return nil
}

// checkServerConfig reads server.yaml the way omo reads its start options.
func checkServerConfig(scratch string, data []byte) error {
	p := filepath.Join(scratch, "server.yaml")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return err
	}
	if _, err := serverconfig.Load(p); err != nil {
		msg := strings.TrimPrefix(strings.ReplaceAll(err.Error(), p, "server.yaml"), "server.yaml: ")
		return problem("server.yaml: %s", msg)
	}
	return nil
}

// change says what writing data to rel would do.
func (h Home) change(rel string, data []byte) string {
	max, _ := h.limit(rel)
	cur, err := homefile.Read(filepath.Join(h.Layout.Root, filepath.FromSlash(rel)), max)
	switch {
	case os.IsNotExist(err):
		return api.ImportAdd
	case err == nil && bytes.Equal(cur, data):
		return api.ImportSame
	default:
		return api.ImportReplace
	}
}

// ApplyOptions choose what Apply writes besides the settings files.
type ApplyOptions struct {
	// Server also writes server.yaml.
	Server bool
	// Database places the zip's database next to omo.db; it replaces it at
	// the next start (SwapPendingDatabase).
	Database bool
	// Ready is called once every check passed, right before the first
	// change; its error stops the import with nothing changed. nil: none.
	Ready func() error
	Now   time.Time
}

// Apply writes the waiting zip id into the home. It checks the zip again
// (the home may have changed since Stage), then: the database goes next to
// omo.db when asked; the settings files the zip holds (server.yaml only when
// asked) are written to temporary names, the files they replace are copied
// into a new folder below BackupFolder, and the temporary files are renamed
// into place, all while the settings store lets no other write in. When a
// rename fails, the files already renamed are put back and the database
// goes back to the waiting zip, so nothing is half applied and the zip can
// be applied again. Files of the allowlist
// the zip does not hold are left as they are. The store then loads the
// written files and reports the changes as its own, as after a save.
func (h Home) Apply(ctx context.Context, id string, o ApplyOptions) (api.ImportResult, error) {
	res := api.ImportResult{Written: []string{}}
	if !idPattern.MatchString(id) {
		return res, ErrNoUpload
	}
	dir := filepath.Join(h.staging(), id)
	if _, err := os.Lstat(filepath.Join(dir, "upload.zip")); err != nil {
		return res, ErrNoUpload
	}
	a, err := h.check(ctx, dir, false)
	if err != nil {
		return res, err
	}
	if o.Database && a.database == nil {
		return res, problem("the zip holds no database")
	}
	var writes []string
	for _, rel := range sortedKeys(a.files) {
		if e, _ := Allowed(rel); e.Server && !o.Server {
			continue
		}
		if h.change(rel, a.files[rel]) != api.ImportSame {
			writes = append(writes, rel)
		}
	}
	if o.Ready != nil {
		if err := o.Ready(); err != nil {
			return res, err
		}
	}
	pending := filepath.Join(h.Layout.Root, PendingDatabase)
	staged := filepath.Join(dir, DatabaseName)
	// unplace puts the database back into the waiting zip's folder after a
	// failure, so that the next try finds it there.
	unplace := func() {
		if o.Database {
			if err := os.Rename(pending, staged); err != nil {
				os.Remove(pending)
			}
		}
	}
	if o.Database {
		if err := os.Rename(staged, pending); err != nil {
			return res, fmt.Errorf("place the imported database: %w", err)
		}
	}
	var replaced []string
	for _, rel := range writes {
		if h.change(rel, a.files[rel]) == api.ImportReplace {
			replaced = append(replaced, rel)
		}
	}
	backup := ""
	if len(replaced) > 0 {
		if backup, err = newBackupFolder(h.Layout.Root, o.Now, "import"); err == nil {
			err = copyFiles(h.Layout.Root, backup, replaced, h.limit)
		}
		if err != nil {
			unplace()
			return res, fmt.Errorf("keep a copy of the replaced files: %w", err)
		}
	}
	err = h.Settings.Replace(writes, func() error {
		return replaceFiles(h.Layout.Root, backup, writes, a.files)
	})
	if err != nil {
		unplace()
		return res, err
	}
	os.RemoveAll(h.staging())
	res.Written = append(res.Written, writes...)
	res.Backup = backup
	res.DatabasePending = o.Database
	res.RestartNeeded = o.Database || (o.Server && contains(writes, "server.yaml"))
	return res, nil
}

// copyFiles copies the files rels of root into backup, keeping their paths.
func copyFiles(root, backup string, rels []string, limit limitFunc) error {
	for _, rel := range rels {
		max, _ := limit(rel)
		data, err := homefile.Read(filepath.Join(root, filepath.FromSlash(rel)), max)
		if err != nil {
			return err
		}
		dst := filepath.Join(backup, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// replaceFiles writes files[rel] for each of rels below root: first every
// file to a hidden temporary name next to its target, then every rename.
// When a rename fails, the targets renamed so far get their earlier content
// back from backup (or are removed when they did not exist).
func replaceFiles(root, backup string, rels []string, files map[string][]byte) error {
	temps := make([]string, len(rels))
	defer func() {
		for _, t := range temps {
			if t != "" {
				os.Remove(t)
			}
		}
	}()
	for i, rel := range rels {
		target := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		t, err := writeTemp(filepath.Dir(target), files[rel])
		if err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
		temps[i] = t
	}
	for i, rel := range rels {
		target := filepath.Join(root, filepath.FromSlash(rel))
		if err := renameFile(temps[i], target); err != nil {
			restore(root, backup, rels[:i])
			return fmt.Errorf("replace %s: %w", rel, err)
		}
		temps[i] = ""
	}
	return nil
}

// restore puts back the earlier content of rels from backup, or removes a
// file that had none.
func restore(root, backup string, rels []string) {
	for _, rel := range rels {
		target := filepath.Join(root, filepath.FromSlash(rel))
		if backup != "" {
			if data, err := homefile.Read(filepath.Join(backup, filepath.FromSlash(rel)), homefile.MaxSettingsFile); err == nil {
				globalhome.WriteFileAtomic(target, data, 0o600)
				continue
			}
		}
		os.Remove(target)
	}
}

// writeTemp writes data to a new hidden file in dir, synced, private.
func writeTemp(dir string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, ".omo-import-*")
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(0o600)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// renameFile renames from onto to; tests replace it to fail a rename.
var renameFile = os.Rename

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
