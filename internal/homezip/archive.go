package homezip

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// Limits of what an import reads. They are variables so tests can lower
// them; nothing else changes them.
var (
	// MaxDatabase bounds the database in a zip, uncompressed.
	MaxDatabase int64 = 4 << 30
	// MaxArchive bounds the uploaded zip.
	MaxArchive = MaxDatabase + 64<<20
	// MaxEntries bounds the number of entries of a zip.
	MaxEntries = 2000
	// MaxSettingsBytes bounds the settings files of a zip together,
	// uncompressed.
	MaxSettingsBytes int64 = 64 << 20
)

// archive is the content of a zip that passed the structural checks: a
// readable manifest of a format this omo knows, only allowlisted paths,
// nothing but regular files (and folders of the allowlist), sizes within
// the limits. Whether each settings file loads is checked separately.
type archive struct {
	manifest api.BackupManifest
	// files are the settings files (server.yaml included) by path.
	files map[string][]byte
	// database is the zip's database entry, nil without one.
	database *zip.File
}

// limitFunc returns the largest size a settings file at rel may have; false
// means it is not a settings file this omo knows.
type limitFunc func(rel string) (int64, bool)

// readArchive checks the zip and reads its settings files. version is this
// omo's version, for the manifest check.
func readArchive(ra io.ReaderAt, size int64, version string, limit limitFunc) (*archive, error) {
	zr, err := zip.NewReader(ra, size)
	if errors.Is(err, zip.ErrInsecurePath) {
		return nil, problem("the zip holds a path that leaves its folder (an absolute path or ..); it is refused")
	}
	if err != nil {
		return nil, problem("the file is not a zip archive omo can read")
	}
	if len(zr.File) > MaxEntries {
		return nil, problem("the zip has %d entries; at most %d are accepted", len(zr.File), MaxEntries)
	}
	a := &archive{files: map[string][]byte{}}
	seen := map[string]bool{}
	var manifest *zip.File
	var total int64
	dirs := folders()
	for _, f := range zr.File {
		name := f.Name
		if err := checkName(name); err != nil {
			return nil, err
		}
		folded := strings.ToLower(strings.TrimSuffix(name, "/"))
		if seen[folded] {
			return nil, problem("the zip holds %s twice", quoted(name))
		}
		seen[folded] = true
		mode := f.Mode()
		if strings.HasSuffix(name, "/") || mode.IsDir() {
			// Zip tools add an entry for each folder; only the
			// allowlist's folders may appear.
			if !dirs[strings.TrimSuffix(name, "/")] {
				return nil, problem("the zip holds the folder %s, which is not part of omo's settings", quoted(name))
			}
			continue
		}
		if mode&fs.ModeSymlink != 0 {
			return nil, problem("the zip holds a link at %s; links are refused", quoted(name))
		}
		if !mode.IsRegular() {
			return nil, problem("the zip holds %s, which is not a plain file", quoted(name))
		}
		switch name {
		case ManifestName:
			manifest = f
			continue
		case DatabaseName:
			if f.UncompressedSize64 > uint64(MaxDatabase) {
				return nil, problem("the database in the zip is larger than %s", homefile.Size(MaxDatabase))
			}
			a.database = f
			continue
		}
		if _, ok := Allowed(name); !ok {
			return nil, problem("the zip holds %s, which is not one of omo's settings files", quoted(name))
		}
		max, ok := limit(name)
		if !ok {
			return nil, problem("the zip holds %s, which is not one of omo's settings files", quoted(name))
		}
		if f.UncompressedSize64 > uint64(max) {
			return nil, problem("%s is larger than %s", quoted(name), homefile.Size(max))
		}
		total += int64(f.UncompressedSize64)
		if total > MaxSettingsBytes {
			return nil, problem("the settings files of the zip are larger than %s together", homefile.Size(MaxSettingsBytes))
		}
		data, err := readEntry(f, max)
		if err != nil {
			return nil, err
		}
		a.files[name] = data
	}
	if manifest == nil {
		return nil, problem("the zip has no %s: it is not an omo settings zip", ManifestName)
	}
	raw, err := readEntry(manifest, maxManifest)
	if err != nil {
		return nil, err
	}
	if a.manifest, err = parseManifest(raw, version); err != nil {
		return nil, err
	}
	if a.manifest.Database != (a.database != nil) {
		return nil, problem("the manifest and the zip disagree on whether the database is inside")
	}
	return a, nil
}

// readEntry reads one entry of at most max bytes; a zip that declares a
// smaller size than it holds is refused by archive/zip itself.
func readEntry(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, problem("%s cannot be read from the zip", quoted(f.Name))
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, max+1))
	if err != nil {
		return nil, problem("%s cannot be read from the zip", quoted(f.Name))
	}
	if int64(len(data)) > max {
		return nil, problem("%s is larger than %s", quoted(f.Name), homefile.Size(max))
	}
	return data, nil
}

// checkName refuses a name that could point outside the home or reads
// differently on another system: empty, absolute, with a drive, a
// backslash, a dot segment, a doubled slash or characters that are not
// plain text.
func checkName(name string) error {
	trimmed := strings.TrimSuffix(name, "/")
	switch {
	case trimmed == "" || !utf8.ValidString(name):
		return problem("the zip holds an entry without a readable name")
	case strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") || path.IsAbs(name):
		return problem("the zip holds the path %s, which could point outside omo's home; it is refused", quoted(name))
	case path.Clean(trimmed) != trimmed || trimmed == ".." || strings.HasPrefix(trimmed, "../") || strings.Contains(trimmed, "/../"):
		return problem("the zip holds the path %s, which could point outside omo's home; it is refused", quoted(name))
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return problem("the zip holds a path with control characters; it is refused")
		}
	}
	return nil
}

// quoted is a path for a message, quoted and escaped, at most 120
// characters.
func quoted(name string) string {
	if utf8.RuneCountInString(name) > 120 {
		name = string([]rune(name)[:120]) + "…"
	}
	return strconv.QuoteToASCII(name)
}
