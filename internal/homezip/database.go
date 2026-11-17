package homezip

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
)

// snapshotDatabase writes a consistent copy of the live database at src to
// dst with VACUUM INTO, through a read-only connection of its own: a read
// transaction of SQLite's write-ahead log sees one committed state, so the
// copy is consistent while omo keeps writing, and the server's own
// connection is not held up. The live file and its log are never copied as
// files.
func snapshotDatabase(ctx context.Context, src, dst string) error {
	d, err := sql.Open("sqlite", "file:"+uriPath(src)+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	defer d.Close()
	d.SetMaxOpenConns(1)
	if _, err := d.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	return nil
}

// uriPath escapes a file path for an SQLite file: URI.
func uriPath(p string) string {
	return (&url.URL{Path: filepath.ToSlash(p)}).EscapedPath()
}

// checkDatabase opens the database file at p read-only and checks that it
// is an intact omo database this omo can open: SQLite's quick check passes,
// its schema version is one omo knows (an older one is migrated at the
// start), and it has omo's tables.
func checkDatabase(ctx context.Context, p string) error {
	d, err := sql.Open("sqlite", "file:"+uriPath(p)+"?mode=ro&immutable=1")
	if err != nil {
		return problem("the database in the zip cannot be opened")
	}
	defer d.Close()
	d.SetMaxOpenConns(1)
	var check string
	if err := d.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return problem("the database in the zip is damaged or not an SQLite database")
	}
	var version int
	if err := d.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return problem("the database in the zip cannot be read")
	}
	switch {
	case version < 1:
		return problem("the database in the zip is not an omo database")
	case version > db.SchemaVersion:
		return problem("the database in the zip was made by a newer omo (schema %d, this omo knows %d); update omo to import it", version, db.SchemaVersion)
	}
	var tables int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('tickets', 'comments', 'events')").Scan(&tables); err != nil || tables != 3 {
		return problem("the database in the zip is not an omo database")
	}
	return nil
}

// SwapPendingDatabase puts an imported database in place before omo opens
// its database: when PendingDatabase exists, the current omo.db and its
// write-ahead log and index move to a new folder below BackupFolder, and the
// imported file becomes omo.db. It must run before the database is opened,
// while no other omo uses the home (the server's start holds the service
// lock). It returns the folder the old database went to, "" when nothing
// was pending. A start that stopped half-way finishes the swap the next
// time: the old files are moved only while they exist.
func SwapPendingDatabase(l globalhome.Layout, now time.Time) (string, error) {
	pending := filepath.Join(l.Root, PendingDatabase)
	info, err := os.Lstat(pending)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a plain file; remove it to start", pending)
	}
	dir, err := newBackupFolder(l.Root, now, "database")
	if err != nil {
		return "", err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from := l.DB + suffix
		if _, err := os.Lstat(from); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.Rename(from, filepath.Join(dir, filepath.Base(from))); err != nil {
			return "", fmt.Errorf("move the old database aside: %w", err)
		}
	}
	if err := os.Rename(pending, l.DB); err != nil {
		return "", fmt.Errorf("put the imported database in place: %w", err)
	}
	return dir, nil
}

// newBackupFolder creates <root>/backups/<time>-<what>, with -2, -3 and so
// on when that name is taken.
func newBackupFolder(root string, now time.Time, what string) (string, error) {
	base := filepath.Join(root, BackupFolder)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	stamp := now.Local().Format("2006-01-02-150405") + "-" + what
	for i := 1; i < 100; i++ {
		name := stamp
		if i > 1 {
			name = fmt.Sprintf("%s-%d", stamp, i)
		}
		dir := filepath.Join(base, name)
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("no free backup folder name for %s", stamp)
}
