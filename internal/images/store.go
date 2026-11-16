package images

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// tempPrefix starts the temporary files of an atomic write; they are never
// counted as stray files.
const tempPrefix = ".omo-write-"

// Store is the images folder of the omo home. Paths are only ever built from
// an id that passed ValidID and a content type omo accepts.
type Store struct {
	dir string
}

// NewStore returns the store of the folder dir (globalhome.Layout.Images).
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir is the folder.
func (s *Store) Dir() string { return s.dir }

// Path returns the absolute path of the file of image id with type ct, or ""
// for an invalid id or a type omo does not accept.
func (s *Store) Path(id, ct string) string {
	if !ValidID(id) || Extension(ct) == "" {
		return ""
	}
	return filepath.Join(s.dir, FileName(id, ct))
}

// ErrInvalid is returned for an id or type no file can belong to.
var ErrInvalid = errors.New("invalid image id or type")

// Write stores data as the file of image id: written to a temporary file
// in the folder (mode 0600) and renamed into place, so a reader never sees
// part of it.
func (s *Store) Write(id, ct string, data []byte) error {
	p := s.Path(id, ct)
	if p == "" {
		return ErrInvalid
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return globalhome.WriteFileAtomic(p, data, 0o600)
}

// Read returns the file of image id: a regular file reached without
// following a link at its name, at most MaxBytes long. A link, a pipe, a
// device or a larger file is refused (homefile.ErrLink, ErrNotRegular,
// ErrTooLarge) as if it were not there for the caller: serve nothing.
func (s *Store) Read(id, ct string) ([]byte, error) {
	p := s.Path(id, ct)
	if p == "" {
		return nil, ErrInvalid
	}
	return homefile.Read(p, MaxBytes)
}

// Present reports whether the file of image id is a regular file of at most
// MaxBytes (no link followed).
func (s *Store) Present(id, ct string) bool {
	p := s.Path(id, ct)
	if p == "" {
		return false
	}
	info, err := os.Lstat(p)
	return err == nil && info.Mode().IsRegular() && info.Size() <= MaxBytes
}

// Remove deletes the file of image id; a file that is already gone is no
// error. A link in its place is removed itself, never what it points to.
func (s *Store) Remove(id, ct string) error {
	p := s.Path(id, ct)
	if p == "" {
		return ErrInvalid
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Stray counts the entries of the folder that are not the file of a known
// image (known gets the entry's name, for example "<id>.png"). Nothing is
// deleted: omo removes only files it wrote for a record. A missing folder
// has none.
func (s *Store) Stray(known func(name string) bool) (int, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) {
			continue
		}
		if !known(e.Name()) {
			n++
		}
	}
	return n, nil
}
