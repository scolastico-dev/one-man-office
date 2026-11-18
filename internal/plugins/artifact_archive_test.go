package plugins

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"github.com/scolastico-dev/one-man-office/internal/boundedzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type archiveMember struct {
	name, body, link string
	kind             byte
	mode             int64
}

func tarFixture(t *testing.T, members ...archiveMember) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		k := m.kind
		if k == 0 {
			k = tar.TypeReg
		}
		mode := m.mode
		if mode == 0 {
			mode = 0o644
		}
		size := int64(len(m.body))
		if k != tar.TypeReg {
			size = 0
		}
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: mode, Size: size, Typeflag: k, Linkname: m.link}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := tw.Write([]byte(m.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func zipFixture(t *testing.T, members ...archiveMember) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, m := range members {
		h := &zip.FileHeader{Name: m.name, Method: zip.Deflate}
		mode := os.FileMode(m.mode)
		if mode == 0 {
			mode = 0o644
		}
		if m.kind == tar.TypeSymlink {
			mode |= os.ModeSymlink
		}
		h.SetMode(mode)
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		body := m.body
		if m.link != "" {
			body = m.link
		}
		if _, err = w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func extractFixture(t *testing.T, raw []byte, format string, strip int, limits artifactLimits) (string, error) {
	t.Helper()
	base := t.TempDir()
	a := filepath.Join(base, "archive")
	if err := os.WriteFile(a, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(base, "tree")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	err := extractArtifact(context.Background(), a, dest, format, strip, limits)
	return dest, err
}
func TestArtifactArchiveFormatsAndLinks(t *testing.T) {
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			members := []archiveMember{{name: "pkg/lib/run", body: "executable", mode: 0o6755}, {name: "pkg/bin/run", link: "../lib/run", kind: tar.TypeSymlink}}
			var raw []byte
			if format == "zip" {
				raw = zipFixture(t, members...)
			} else {
				raw = tarFixture(t, members...)
			}
			dir, err := extractFixture(t, raw, format, 1, defaultArtifactLimits())
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" {
				if info, err := os.Lstat(filepath.Join(dir, "bin/run")); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("contained links must preserve realpath semantics", err)
				}
			}
			b, err := os.ReadFile(filepath.Join(dir, "bin/run"))
			if err != nil || string(b) != "executable" {
				t.Fatalf("link: %s %v", b, err)
			}
			info, err := os.Lstat(filepath.Join(dir, "lib/run"))
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
				t.Fatalf("unsafe or lost executable mode: %v", info.Mode())
			}
		})
	}
	dir, err := extractFixture(t, tarFixture(t, archiveMember{name: "pkg/hard", link: "pkg/file", kind: tar.TypeLink}, archiveMember{name: "pkg/file", body: "ok"}), "tar.gz", 1, defaultArtifactLimits())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "hard"))
	if string(b) != "ok" {
		t.Fatal("forward hardlink lost")
	}
}
func TestArtifactArchiveRejectsUnsafeMembers(t *testing.T) {
	paths := []string{"../outside", "pkg/../../outside", "/absolute", "C:/drive", "C:relative", `\\host\share\file`, `pkg\..\outside`, "pkg/%2e%2e/outside", "pkg/%252e%252e/outside", "pkg/file:stream", "pkg/NUL.txt", "pkg/COM1", "pkg/dot.", "pkg/space ", "pkg/\x00bad"}
	for _, name := range paths {
		for _, format := range []string{"tar.gz", "zip"} {
			t.Run(format+"/"+strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
				if strings.ContainsRune(name, 0) && format == "tar.gz" {
					return
				}
				m := archiveMember{name: name, body: "bad"}
				var raw []byte
				if format == "zip" {
					raw = zipFixture(t, m)
				} else {
					raw = tarFixture(t, m)
				}
				if _, err := extractFixture(t, raw, format, 0, defaultArtifactLimits()); err == nil {
					t.Fatal("unsafe path accepted")
				}
			})
		}
	}
	for _, m := range []archiveMember{{name: "pkg/a", link: "../../escape", kind: tar.TypeSymlink}, {name: "pkg/a", link: "/etc/passwd", kind: tar.TypeSymlink}, {name: "pkg/a", link: "C:/drive", kind: tar.TypeLink}, {name: "pkg/a", link: "missing", kind: tar.TypeSymlink}, {name: "pkg/a", link: "a", kind: tar.TypeSymlink}, {name: "pkg/device", kind: tar.TypeChar}, {name: "pkg/pipe", kind: tar.TypeFifo}} {
		if _, err := extractFixture(t, tarFixture(t, m), "tar.gz", 1, defaultArtifactLimits()); err == nil {
			t.Fatalf("unsafe member accepted: %+v", m)
		}
	}
	// No archive can write through a link, including links to a contained directory.
	raw := tarFixture(t, archiveMember{name: "pkg/link", link: "dir", kind: tar.TypeSymlink}, archiveMember{name: "pkg/link/file", body: "overwrite"}, archiveMember{name: "pkg/dir/", kind: tar.TypeDir})
	if _, err := extractFixture(t, raw, "tar.gz", 1, defaultArtifactLimits()); err == nil {
		t.Fatal("write through link accepted")
	}
}
func TestArtifactArchiveBoundsAndIntegrity(t *testing.T) {
	raw := tarFixture(t, archiveMember{name: "pkg/file", body: "12345"})
	for _, change := range []func(*artifactLimits){func(l *artifactLimits) { l.file = 4 }, func(l *artifactLimits) { l.expanded = 4 }, func(l *artifactLimits) { l.entries = 1 }} {
		l := defaultArtifactLimits()
		change(&l)
		data := raw
		if l.entries == 1 {
			data = tarFixture(t, archiveMember{name: "pkg/a", body: "a"}, archiveMember{name: "pkg/b", body: "b"})
		}
		if _, err := extractFixture(t, data, "tar.gz", 1, l); err == nil {
			t.Fatal("archive bound ignored")
		}
	}
	dir, err := extractFixture(t, raw, "tar.gz", 1, defaultArtifactLimits())
	if err != nil {
		t.Fatal(err)
	}
	first, err := artifactTreeHash(context.Background(), dir, defaultArtifactLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("54321"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := artifactTreeHash(context.Background(), dir, defaultArtifactLimits())
	if err != nil || first == second {
		t.Fatal("same-size content tamper undetected", err)
	}
}

func TestArtifactArchivePublisherBinLinks(t *testing.T) {
	pairs := map[string]string{"escodegen": "../escodegen/bin/escodegen.js", "node-gyp-build-optional": "../node-gyp-build/optional.js", "esgenerate": "../escodegen/bin/esgenerate.js", "node-which": "../which/bin/node-which", "cross-env": "../cross-env/dist/bin/cross-env.js", "esvalidate": "../esprima/bin/esvalidate.js", "node-gyp-build": "../node-gyp-build/bin.js", "node-gyp-build-test": "../node-gyp-build/build-test.js", "cross-env-shell": "../cross-env/dist/bin/cross-env-shell.js", "esparse": "../esprima/bin/esparse.js", "js-yaml": "../js-yaml/bin/js-yaml.mjs", "semver": "../semver/bin/semver.js"}
	var members []archiveMember
	for name, target := range pairs {
		members = append(members, archiveMember{name: "pkg/node_modules/.bin/" + name, link: target, kind: tar.TypeSymlink}, archiveMember{name: "pkg/node_modules/" + strings.TrimPrefix(target, "../"), body: "fixture:" + name, mode: 0o755})
	}
	dir, err := extractFixture(t, tarFixture(t, members...), "tar.gz", 1, defaultArtifactLimits())
	if err != nil {
		t.Fatal(err)
	}
	for name, target := range pairs {
		file := filepath.Join(dir, "node_modules", ".bin", name)
		b, err := os.ReadFile(file)
		if err != nil || string(b) != "fixture:"+name {
			t.Fatal(name, err)
		}
		if runtime.GOOS != "windows" {
			actual, err := os.Readlink(file)
			if err != nil || filepath.ToSlash(actual) != target {
				t.Fatal("changed publisher link", name, actual, err)
			}
		}
	}
	if _, err = artifactTreeHash(t.Context(), dir, defaultArtifactLimits()); err != nil {
		t.Fatal(err)
	}
}
func TestArtifactArchiveIntegrityIncludesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	dir, err := extractFixture(t, tarFixture(t, archiveMember{name: "pkg/run", body: "fixture", mode: 0o755}), "tar.gz", 1, defaultArtifactLimits())
	if err != nil {
		t.Fatal(err)
	}
	before, err := artifactTreeHash(t.Context(), dir, defaultArtifactLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(dir, "run"), 0o777); err != nil {
		t.Fatal(err)
	}
	after, err := artifactTreeHash(t.Context(), dir, defaultArtifactLimits())
	if err == nil && before == after {
		t.Fatal("cache permission tamper undetected")
	}
}
func TestArtifactArchiveCollisionsAndLinkChains(t *testing.T) {
	for _, members := range [][]archiveMember{
		{{name: "pkg/A", body: "a"}, {name: "pkg/a", body: "b"}},
		{{name: "pkg/Dir/a", body: "a"}, {name: "pkg/dir/b", body: "b"}},
		{{name: "pkg/file", body: "a"}, {name: "pkg/file", body: "b"}},
		{{name: "pkg/dir/file", body: "a"}, {name: "pkg/dir", link: "file", kind: tar.TypeSymlink}},
		{{name: "pkg/a", link: "b", kind: tar.TypeSymlink}, {name: "pkg/b", link: "a", kind: tar.TypeSymlink}},
		{{name: "pkg/a", link: "%2e%2e/escape", kind: tar.TypeSymlink}},
		{{name: "pkg/a", link: `..\escape`, kind: tar.TypeSymlink}},
	} {
		if _, err := extractFixture(t, tarFixture(t, members...), "tar.gz", 1, defaultArtifactLimits()); err == nil {
			t.Fatal("unsafe collision/link accepted", members)
		}
	}
}

func TestArtifactArchiveIntegrityIncludesRootMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	dir, err := extractFixture(t, tarFixture(t, archiveMember{name: "pkg/file", body: "fixture"}), "tar.gz", 1, defaultArtifactLimits())
	if err != nil {
		t.Fatal(err)
	}
	before, err := artifactTreeHash(t.Context(), dir, defaultArtifactLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	after, err := artifactTreeHash(t.Context(), dir, defaultArtifactLimits())
	if err == nil && before == after {
		t.Fatal("cache root permission tamper undetected")
	}
}

func TestArtifactZIPPreflightBoundsBeforeParsing(t *testing.T) {
	raw := zipFixture(t, archiveMember{name: "pkg/a", body: "a"}, archiveMember{name: "pkg/b", body: "b"}, archiveMember{name: "pkg/c", body: "c"})
	l := defaultArtifactLimits()
	l.entries = 2
	if err := boundedzip.Check(t.Context(), bytes.NewReader(raw), int64(len(raw)), boundedzip.Limits{ArchiveBytes: l.compressed, Entries: l.entries}); err == nil {
		t.Fatal("entry bound not applied before ZIP reader construction")
	}
	// The EOCD count is untrusted: a forged smaller count must not bypass scan.
	forged := append([]byte{}, raw...)
	end := bytes.LastIndex(forged, []byte{'P', 'K', 5, 6})
	binary.LittleEndian.PutUint16(forged[end+8:], 1)
	binary.LittleEndian.PutUint16(forged[end+10:], 1)
	if err := boundedzip.Check(t.Context(), bytes.NewReader(forged), int64(len(forged)), boundedzip.Limits{ArchiveBytes: l.compressed, Entries: l.entries}); err == nil {
		t.Fatal("trusted forged EOCD count")
	}
	l = defaultArtifactLimits()
	if err := boundedzip.Check(t.Context(), bytes.NewReader(raw), int64(len(raw)), boundedzip.Limits{ArchiveBytes: l.compressed, Entries: l.entries}); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(forged[end+12:], uint32(boundedzip.MaxDirectoryBytes+1))
	if err := boundedzip.Check(t.Context(), bytes.NewReader(forged), int64(len(forged)), boundedzip.Limits{ArchiveBytes: l.compressed, Entries: l.entries}); err == nil {
		t.Fatal("oversized ZIP metadata accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := boundedzip.Check(ctx, bytes.NewReader(raw), int64(len(raw)), boundedzip.Limits{ArchiveBytes: l.compressed, Entries: l.entries}); err == nil {
		t.Fatal("ZIP preflight ignores cancellation")
	}
}

func TestArtifactZIP64BoundedDirectory(t *testing.T) {
	raw := zipFixture(t, archiveMember{name: "pkg/file", body: "fixture"})
	end := bytes.LastIndex(raw, []byte{'P', 'K', 5, 6})
	eocd := append([]byte{}, raw[end:]...)
	record := make([]byte, 56)
	binary.LittleEndian.PutUint32(record, 0x06064b50)
	binary.LittleEndian.PutUint64(record[4:], 44)
	binary.LittleEndian.PutUint16(record[12:], 45)
	binary.LittleEndian.PutUint16(record[14:], 45)
	binary.LittleEndian.PutUint64(record[24:], uint64(binary.LittleEndian.Uint16(eocd[8:])))
	binary.LittleEndian.PutUint64(record[32:], uint64(binary.LittleEndian.Uint16(eocd[10:])))
	binary.LittleEndian.PutUint64(record[40:], uint64(binary.LittleEndian.Uint32(eocd[12:])))
	binary.LittleEndian.PutUint64(record[48:], uint64(binary.LittleEndian.Uint32(eocd[16:])))
	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator, 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:], uint64(end))
	binary.LittleEndian.PutUint32(locator[16:], 1)
	binary.LittleEndian.PutUint16(eocd[8:], 0xffff)
	binary.LittleEndian.PutUint16(eocd[10:], 0xffff)
	binary.LittleEndian.PutUint32(eocd[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(eocd[16:], 0xffffffff)
	zipped := append([]byte{}, raw[:end]...)
	zipped = append(zipped, record...)
	zipped = append(zipped, locator...)
	zipped = append(zipped, eocd...)
	dir, err := extractFixture(t, zipped, "zip", 1, defaultArtifactLimits())
	if err != nil {
		t.Fatal("bounded ZIP64 rejected", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil || string(b) != "fixture" {
		t.Fatal("ZIP64 extraction", err)
	}
	ambiguous := append([]byte{}, zipped...)
	eocdStart := len(ambiguous) - 22
	binary.LittleEndian.PutUint16(ambiguous[eocdStart+8:], 1)
	binary.LittleEndian.PutUint16(ambiguous[eocdStart+10:], 1)
	binary.LittleEndian.PutUint32(ambiguous[eocdStart+16:], uint32(binary.LittleEndian.Uint64(record[48:])))
	if err := boundedzip.Check(t.Context(), bytes.NewReader(ambiguous), int64(len(ambiguous)), boundedzip.Limits{ArchiveBytes: MaxArtifactDownloadBytes, Entries: MaxArtifactEntries}); err == nil {
		t.Fatal("ambiguous ZIP64 sentinel layout accepted")
	}
	binary.LittleEndian.PutUint64(zipped[end+32:], 50001)
	if _, err = extractFixture(t, zipped, "zip", 1, defaultArtifactLimits()); err == nil {
		t.Fatal("ZIP64 entry overflow accepted")
	}
}

func TestArtifactZIPExtractionLimits(t *testing.T) {
	raw := zipFixture(t, archiveMember{name: "pkg/file", body: "12345"})
	for _, change := range []func(*artifactLimits){func(l *artifactLimits) { l.file = 4 }, func(l *artifactLimits) { l.expanded = 4 }, func(l *artifactLimits) { l.entries = 1 }} {
		l := defaultArtifactLimits()
		change(&l)
		data := raw
		if l.entries == 1 {
			data = zipFixture(t, archiveMember{name: "pkg/a", body: "a"}, archiveMember{name: "pkg/b", body: "b"})
		}
		if _, err := extractFixture(t, data, "zip", 1, l); err == nil {
			t.Fatal("ZIP extraction limit ignored")
		}
	}
}

func TestArtifactZIPRejectsAmbiguousEndRecord(t *testing.T) {
	raw := zipFixture(t, archiveMember{name: "pkg/file", body: "fixture"})
	// Standard archive/zip chooses the last EOCD signature, even with trailing
	// comment bytes. Preflight must not validate a different earlier EOCD.
	end := bytes.LastIndex(raw, []byte{'P', 'K', 5, 6})
	comment := make([]byte, 23)
	copy(comment, []byte{'P', 'K', 5, 6})
	comment[22] = 'x'
	binary.LittleEndian.PutUint16(raw[end+20:], uint16(len(comment)))
	raw = append(raw, comment...)
	if err := boundedzip.Check(t.Context(), bytes.NewReader(raw), int64(len(raw)), boundedzip.Limits{ArchiveBytes: MaxArtifactDownloadBytes, Entries: MaxArtifactEntries}); err == nil {
		t.Fatal("ambiguous ZIP end records passed preflight")
	}
}
