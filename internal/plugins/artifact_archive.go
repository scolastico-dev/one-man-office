package plugins

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/boundedzip"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

const (
	MaxArtifactDownloadBytes int64 = 512 << 20
	MaxArtifactExpandedBytes int64 = 2 << 30
	MaxArtifactFileBytes     int64 = 512 << 20
	MaxArtifactEntries             = 50000
	ArtifactTimeout                = 30 * time.Minute
)

type artifactLimits struct {
	compressed, expanded, file int64
	entries                    int
	timeout                    time.Duration
}

func defaultArtifactLimits() artifactLimits {
	return artifactLimits{MaxArtifactDownloadBytes, MaxArtifactExpandedBytes, MaxArtifactFileBytes, MaxArtifactEntries, ArtifactTimeout}
}

var errArchive = errors.New("artifact archive is invalid, unsafe or exceeds extraction limits")

// archivePath uses the same portable syntax on every host. Decode ambiguity,
// Windows devices/ADS and special characters are refused before stripping.
func archivePath(s string, parents bool) (string, error) {
	if s == "" || len(s) > 4096 || !utf8.ValidString(s) || strings.HasPrefix(s, "/") || strings.ContainsAny(s, "\\:%\x00<>\"|?*") || strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || textsafe.IsHidden(r) }) {
		return "", errArchive
	}
	for strings.HasPrefix(s, "./") {
		s = strings.TrimPrefix(s, "./")
	}
	s = strings.TrimSuffix(s, "/")
	if s == "." || s == "" {
		return ".", nil
	}
	for _, p := range strings.Split(s, "/") {
		if p == ".." && parents {
			continue
		}
		if p == "" || p == ".." || p == "." || len(p) > 255 || strings.HasSuffix(p, ".") || strings.HasSuffix(p, " ") {
			return "", errArchive
		}
		stem, _, _ := strings.Cut(strings.ToUpper(p), ".")
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || ((strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && len([]rune(stem)) == 4 && strings.ContainsAny(string([]rune(stem)[3]), "123456789¹²³")) {
			return "", errArchive
		}
	}
	return s, nil
}
func strippedArchivePath(s string, strip int) (string, error) {
	clean, err := archivePath(s, false)
	if err != nil {
		return "", err
	}
	if clean == "." {
		return "", nil
	}
	parts := strings.Split(clean, "/")
	if len(parts) <= strip {
		return "", nil
	}
	return strings.Join(parts[strip:], "/"), nil
}

type archiveLink struct{ name, target string }
type artifactExtractor struct {
	ctx    context.Context
	root   *os.Root
	dir    string
	limits artifactLimits
	total  int64
	count  int
	names  map[string]string
	files  map[string]bool
	links  []archiveLink
}

func safeArtifactMode(m os.FileMode) os.FileMode {
	if m&0o111 != 0 {
		return 0o700
	}
	return 0o600
}
func (e *artifactExtractor) member(name string, folder bool) error {
	if err := e.ctx.Err(); err != nil {
		return err
	}
	e.count++
	if e.count > e.limits.entries {
		return errArchive
	}
	if name == "" {
		return nil
	}
	fold := strings.ToLower(name)
	kind := "file"
	if folder {
		kind = "dir"
	}
	if prev, ok := e.names[fold]; ok {
		if folder && prev == "dir:"+name {
			return nil
		}
		return errArchive
	}
	// Register implicit parents too: aliases like Dir/a and dir/b collide
	// on Windows/macOS even when the archive has no directory headers.
	for p := path.Dir(name); p != "."; p = path.Dir(p) {
		key := strings.ToLower(p)
		if prev, ok := e.names[key]; ok && prev != "dir:"+p {
			return errArchive
		}
		e.names[key] = "dir:" + p
	}
	e.names[fold] = kind + ":" + name
	if len(e.names) > e.limits.entries {
		return errArchive
	}
	if folder {
		return e.root.MkdirAll(name, 0o700)
	}
	return e.root.MkdirAll(path.Dir(name), 0o700)
}
func (e *artifactExtractor) write(name string, r io.Reader, size int64, mode os.FileMode) error {
	if size < 0 || size > e.limits.file || size > e.limits.expanded-e.total {
		return errArchive
	}
	e.total += size
	f, err := e.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, safeArtifactMode(mode))
	if err != nil {
		return errArchive
	}
	n, err := io.Copy(f, &artifactContextReader{ctx: e.ctx, r: io.LimitReader(r, size+1)})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil || n != size {
		return errArchive
	}
	e.files[name] = true
	return nil
}
func (e *artifactExtractor) link(name, target string, hard bool, strip int) error {
	t, err := archivePath(target, !hard)
	if err != nil {
		return err
	}
	if hard {
		t, err = strippedArchivePath(t, strip)
	} else {
		t = path.Clean(path.Join(path.Dir(name), t))
	}
	if err != nil || t == "" || t == "." || t == ".." || strings.HasPrefix(t, "../") {
		return errArchive
	}
	e.links = append(e.links, archiveLink{name, t})
	return nil
}
func (e *artifactExtractor) finish() error {
	// Deferred copies need no symlink privileges, and no extraction write ever
	// traverses an archive link. Forward links are resolved in bounded passes.
	pending := e.links
	for len(pending) > 0 {
		var next []archiveLink
		for _, l := range pending {
			if err := e.ctx.Err(); err != nil {
				return err
			}
			if !e.files[l.target] {
				next = append(next, l)
				continue
			}
			if runtime.GOOS != "windows" {
				// Keep the publisher's relative link (including Node realpath
				// semantics); all targets are proven regular contained files.
				target, err := filepath.Rel(filepath.FromSlash(path.Dir(l.name)), filepath.FromSlash(l.target))
				if err != nil {
					return errArchive
				}
				if err = e.root.Symlink(target, l.name); err != nil {
					return errArchive
				}
				e.files[l.name] = true
				continue
			}
			f, err := homefile.Open(filepath.Join(e.dir, filepath.FromSlash(l.target)))
			if err != nil {
				return errArchive
			}
			info, err := f.Stat()
			if err == nil {
				err = e.write(l.name, f, info.Size(), info.Mode())
			}
			f.Close()
			if err != nil {
				return errArchive
			}
		}
		if len(next) == len(pending) {
			return errArchive
		}
		pending = next
	}
	return nil
}
func extractArtifact(ctx context.Context, archive, dir, format string, strip int, limits artifactLimits) error {
	f, err := homefile.Open(archive)
	if err != nil {
		return errArchive
	}
	defer f.Close()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return errArchive
	}
	defer root.Close()
	e := &artifactExtractor{ctx: ctx, root: root, dir: dir, limits: limits, names: map[string]string{}, files: map[string]bool{}}
	switch format {
	case "tar.gz":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return errArchive
		}
		defer gz.Close()
		// Bound metadata and skipped entries as well as regular file payloads.
		stream := &io.LimitedReader{R: &artifactContextReader{ctx: ctx, r: gz}, N: limits.expanded + int64(limits.entries)*4096 + 1}
		tr := tar.NewReader(stream)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return errArchive
			}
			name, err := strippedArchivePath(h.Name, strip)
			if err != nil {
				return err
			}
			folder := h.Typeflag == tar.TypeDir
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && !folder && h.Typeflag != tar.TypeSymlink && h.Typeflag != tar.TypeLink {
				return errArchive
			}
			if err = e.member(name, folder); err != nil {
				return err
			}
			if name == "" {
				if !folder {
					return errArchive
				}
				continue
			}
			switch {
			case folder:
			case h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeLink:
				err = e.link(name, h.Linkname, h.Typeflag == tar.TypeLink, strip)
			default:
				err = e.write(name, tr, h.Size, os.FileMode(h.Mode))
			}
			if err != nil {
				return err
			}
		}
		// Check the gzip footer and bound trailing decompressed bytes.
		if _, err := io.Copy(io.Discard, stream); err != nil || stream.N <= 0 {
			return errArchive
		}
	case "zip":
		info, err := f.Stat()
		if err != nil {
			return errArchive
		}
		zr, err := boundedzip.NewReader(ctx, f, info.Size(), boundedzip.Limits{ArchiveBytes: limits.compressed, Entries: limits.entries})
		if err != nil || len(zr.File) > limits.entries {
			return errArchive
		}
		for _, h := range zr.File {
			name, err := strippedArchivePath(h.Name, strip)
			if err != nil {
				return err
			}
			mode := h.Mode()
			folder := mode.IsDir()
			if !folder && !mode.IsRegular() && mode&os.ModeSymlink == 0 {
				return errArchive
			}
			if err = e.member(name, folder); err != nil {
				return err
			}
			if name == "" {
				if !folder {
					return errArchive
				}
				continue
			}
			if folder {
				continue
			}
			if h.UncompressedSize64 > uint64(limits.file) {
				return errArchive
			}
			r, err := h.Open()
			if err != nil {
				return errArchive
			}
			if mode&os.ModeSymlink != 0 {
				b, readErr := io.ReadAll(io.LimitReader(r, 4097))
				if readErr != nil || len(b) > 4096 {
					err = errArchive
				} else {
					err = e.link(name, string(b), false, strip)
				}
			} else {
				err = e.write(name, r, int64(h.UncompressedSize64), mode)
			}
			r.Close()
			if err != nil {
				return err
			}
		}
	default:
		return errArchive
	}
	if len(e.names) == 0 {
		return errArchive
	}
	return e.finish()
}

type artifactContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *artifactContextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

// artifactTreeHash covers every path, file byte and executable bit, including
// materialized links. Cache validation rejects extra/missing/nonregular files.
// Home files are opened through homefile, never an archive-selected read API.
func artifactTreeHash(ctx context.Context, dir string, limits artifactLimits) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", errArchive
	}
	defer root.Close()
	rootInfo, err := os.Lstat(dir)
	if err != nil || !rootInfo.IsDir() {
		return "", errArchive
	}
	var names []string
	var total int64
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errArchive
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return errArchive
		}
		rel = filepath.ToSlash(rel)
		if _, err = archivePath(rel, false); err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() && d.Type()&os.ModeSymlink == 0 {
			return errArchive
		}
		names = append(names, rel)
		if len(names) > limits.entries {
			return errArchive
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	h := sha256.New()
	fmt.Fprintf(h, "root:%o\n", rootInfo.Mode())
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		p := filepath.Join(dir, filepath.FromSlash(name))
		info, err := os.Lstat(p)
		if err != nil {
			return "", errArchive
		}
		if info.IsDir() {
			fmt.Fprintf(h, "d:%d:%s:%o\n", len(name), name, info.Mode())
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := root.Readlink(name)
			if err != nil {
				return "", errArchive
			}
			if _, err = archivePath(filepath.ToSlash(target), true); err != nil {
				return "", errArchive
			}
			resolved, err := root.Stat(name)
			if err != nil || !resolved.Mode().IsRegular() {
				return "", errArchive
			}
			fmt.Fprintf(h, "l:%d:%s:%d:%s:", len(name), name, len(target), target)
			continue
		}
		if !info.Mode().IsRegular() || info.Size() > limits.file || info.Size() > limits.expanded-total {
			return "", errArchive
		}
		total += info.Size()
		f, err := homefile.Open(p)
		if err != nil {
			return "", errArchive
		}
		fmt.Fprintf(h, "f:%d:%s:%d:%o:", len(name), name, info.Size(), info.Mode())
		n, err := io.Copy(h, &artifactContextReader{ctx: ctx, r: io.LimitReader(f, info.Size()+1)})
		f.Close()
		if err != nil || n != info.Size() {
			return "", errArchive
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
