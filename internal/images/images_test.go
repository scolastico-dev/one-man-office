package images

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// PNG returns a w×h PNG; tests of other packages build their uploads the same way.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// webpLossless is the header of a 3×2 lossless WebP (VP8L): enough for Sniff.
func webpLossless(w, h int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBPVP8L\x00\x00\x00\x00\x2f")
	bits := uint32(w-1) | uint32(h-1)<<14
	b = append(b, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	return append(b, make([]byte, 16)...)
}

// webpExtended is the header of an extended WebP (VP8X) of w×h pixels.
func webpExtended(w, h int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00")
	w, h = w-1, h-1
	b = append(b, byte(w), byte(w>>8), byte(w>>16), byte(h), byte(h>>8), byte(h>>16))
	return append(b, make([]byte, 16)...)
}

func TestSniffAcceptsTheFourTypesByContent(t *testing.T) {
	var jp, gf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	if err := jpeg.Encode(&jp, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gf, img, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"png", pngOf(t, 5, 5), TypePNG},
		{"jpeg", jp.Bytes(), TypeJPEG},
		{"gif", gf.Bytes(), TypeGIF},
		{"webp", webpLossless(3, 2), TypeWebP},
		{"webp extended", webpExtended(300, 200), TypeWebP},
	} {
		got, err := Sniff(c.data)
		if err != nil || got != c.want {
			t.Errorf("%s: Sniff = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestSniffRefusesWhatIsNotAPicture(t *testing.T) {
	for name, data := range map[string][]byte{
		"svg":            []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html":           []byte("<!doctype html><script>alert(1)</script>"),
		"empty":          nil,
		"png magic only": []byte("\x89PNG\r\n\x1a\n<script>"),
		"text":           []byte("hello"),
		"riff not webp":  []byte("RIFF\x00\x00\x00\x00AVI LIST"),
	} {
		_, err := Sniff(data)
		var r Refusal
		if !errors.As(err, &r) {
			t.Errorf("%s: Sniff error = %v, want a Refusal", name, err)
		}
	}
	if _, err := Sniff(pngOf(t, MaxSide+1, 1)); err == nil || !strings.Contains(err.Error(), "pixels") {
		t.Errorf("an image wider than MaxSide: %v", err)
	}
	if _, err := Sniff(webpExtended(MaxSide+1, 1)); err == nil {
		t.Error("a WebP wider than MaxSide was accepted")
	}
}

func TestIDs(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := NewID()
		if !ValidID(id) || seen[id] {
			t.Fatalf("NewID = %q (valid %v, repeated %v)", id, ValidID(id), seen[id])
		}
		seen[id] = true
	}
	for _, bad := range []string{"", "../../etc/passwd", "ABCDEF01-2345-4678-9abc-def012345678", "abcdef01-2345-4678-9abc-def01234567", "abcdef01-2345-4678-9abc-def012345678/", "abcdef01-2345-4678-9abc-def012345678.png"} {
		if ValidID(bad) {
			t.Errorf("ValidID(%q) = true", bad)
		}
	}
}

func TestCleanName(t *testing.T) {
	for raw, want := range map[string]string{
		"screenshot.png":         "screenshot.png",
		"../../etc/passwd":       "passwd",
		`C:\Users\me\shot 1.png`: "shot 1.png",
		"a\u202eb.png":           "a b.png",
		"line\nbreak.png":        "line break.png",
		"":                       "image.png",
		"/":                      "image.png",
		"..":                     "image.png",
		strings.Repeat("x", 300): strings.Repeat("x", MaxNameLength),
	} {
		if got := CleanName(raw, TypePNG); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestMarkdownRoundTrip(t *testing.T) {
	id := NewID()
	for _, name := range []string{"shot.png", "a]b[c\\d.png", "x_y*z`w<v>.png"} {
		md := Markdown(name, id)
		refs := Refs("before " + md + " after")
		if len(refs) != 1 || refs[0].ID != id || refs[0].Alt != name {
			t.Errorf("Markdown(%q) = %q, read back as %+v", name, md, refs)
		}
	}
}

func TestAnnotate(t *testing.T) {
	a, b, c := NewID(), NewID(), NewID()
	text := "See ![one.png](omo-image:" + a + ") and ![two.png](omo-image:" + b + ")\n![one.png](omo-image:" + a + ") ![x](omo-image:" + c + ") ![y](omo-image:nope)"
	calls := 0
	got := Annotate(text, func(id string) Note {
		calls++
		switch id {
		case a:
			return Note{Path: "/home/me/.local/omo/images/" + a + ".png"}
		case b:
			return Note{Expired: true}
		}
		return Note{Missing: true}
	})
	want := "See ![one.png](omo-image:" + a + ") (local file: /home/me/.local/omo/images/" + a + ".png) and ![two.png](omo-image:" + b + ") (image expired)\n" +
		"![one.png](omo-image:" + a + ") (local file: /home/me/.local/omo/images/" + a + ".png) ![x](omo-image:" + c + ") (image missing) ![y](omo-image:nope)"
	if got != want {
		t.Errorf("Annotate =\n%s\nwant\n%s", got, want)
	}
	if calls != 3 {
		t.Errorf("look called %d times, want once per id (3)", calls)
	}
	if Annotate("no images", func(string) Note { t.Fatal("looked up"); return Note{} }) != "no images" {
		t.Error("a text without references changed")
	}
}

func TestForExport(t *testing.T) {
	id := NewID()
	got := ForExport("Look:\n\n![screen shot.png](omo-image:" + id + ")\n\nand [a link](https://example.com).")
	want := "Look:\n\n[image: screen shot.png, not exported]\n\nand [a link](https://example.com)."
	if got != want {
		t.Errorf("ForExport = %q, want %q", got, want)
	}
}

func TestStoreWriteReadRemove(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "images"))
	id := NewID()
	data := pngOf(t, 2, 2)
	if err := s.Write(id, TypePNG, data); err != nil {
		t.Fatal(err)
	}
	p := s.Path(id, TypePNG)
	if filepath.Base(p) != id+".png" {
		t.Fatalf("Path = %q", p)
	}
	if info, err := os.Stat(p); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("stored file: %v, %v", info, err)
	}
	got, err := s.Read(id, TypePNG)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("Read = %d bytes, %v", len(got), err)
	}
	if !s.Present(id, TypePNG) {
		t.Fatal("Present = false for a stored file")
	}
	if err := s.Remove(id, TypePNG); err != nil {
		t.Fatal(err)
	}
	if s.Present(id, TypePNG) {
		t.Fatal("Present after Remove")
	}
	if err := s.Remove(id, TypePNG); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	if s.Path("../x", TypePNG) != "" || s.Path(id, "image/svg+xml") != "" {
		t.Fatal("Path built a path from an invalid id or type")
	}
	if err := s.Write("../x", TypePNG, data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Write with an invalid id: %v", err)
	}
}

func TestStoreRefusesPlantedLinkAndOversizedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges on Windows")
	}
	dir := filepath.Join(t.TempDir(), "images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir)
	secret := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(secret, pngOf(t, 1, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := NewID()
	if err := os.Symlink(secret, s.Path(linked, TypePNG)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(linked, TypePNG); !errors.Is(err, homefile.ErrLink) {
		t.Errorf("Read of a planted link: %v, want ErrLink", err)
	}
	if s.Present(linked, TypePNG) {
		t.Error("a link counts as present")
	}
	if err := s.Remove(linked, TypePNG); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Errorf("Remove followed the link: %v", err)
	}

	big := NewID()
	f, err := os.Create(s.Path(big, TypePNG))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := s.Read(big, TypePNG); !errors.Is(err, homefile.ErrTooLarge) {
		t.Errorf("Read of an oversized file: %v, want ErrTooLarge", err)
	}
	if s.Present(big, TypePNG) {
		t.Error("an oversized file counts as present")
	}
}

func TestStray(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "images")
	s := NewStore(dir)
	if n, err := s.Stray(func(string) bool { return false }); n != 0 || err != nil {
		t.Fatalf("missing folder: %d, %v", n, err)
	}
	known := NewID()
	if err := s.Write(known, TypePNG, pngOf(t, 1, 1)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"notes.txt", NewID() + ".png", ".omo-write-123"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Stray(func(name string) bool { return name == known+".png" })
	if err != nil || n != 2 {
		t.Fatalf("Stray = %d, %v; want 2", n, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 4 {
		t.Fatalf("Stray removed files: %d left", len(entries))
	}
}

// The folder of terminal images inside the images folder is not a stray
// file of the images folder.
func TestStrayLeavesTheTerminalFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "images")
	s := NewStore(dir)
	if err := NewStore(filepath.Join(dir, TerminalFolder)).Write(NewID(), TypePNG, pngOf(t, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Stray(func(string) bool { return false }); n != 0 || err != nil {
		t.Fatalf("Stray = %d, %v; want 0", n, err)
	}
}

// RemoveOlder deletes the image files omo wrote that are older than the
// cutoff and leaves everything else.
func TestRemoveOlder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "terminal")
	s := NewStore(dir)
	if n, err := s.RemoveOlder(time.Now()); n != 0 || err != nil {
		t.Fatalf("missing folder: %d, %v", n, err)
	}
	old, fresh := NewID(), NewID()
	for _, id := range []string{old, fresh} {
		if err := s.Write(id, TypePNG, pngOf(t, 1, 1)); err != nil {
			t.Fatal(err)
		}
	}
	foreign := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(foreign, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, NewID()+".png.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	past := now.Add(-48 * time.Hour)
	for _, p := range []string{s.Path(old, TypePNG), foreign} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.RemoveOlder(now.Add(-24 * time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("RemoveOlder = %d, %v; want 1", n, err)
	}
	if s.Present(old, TypePNG) || !s.Present(fresh, TypePNG) {
		t.Fatal("the wrong file went")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("a file omo did not write went: %v", err)
	}
}

func TestTotalBytes(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "terminal"))
	if n, err := s.TotalBytes(); n != 0 || err != nil {
		t.Fatalf("missing folder: %d, %v", n, err)
	}
	data := pngOf(t, 1, 1)
	for range 2 {
		if err := s.Write(NewID(), TypePNG, data); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.TotalBytes(); n != int64(2*len(data)) || err != nil {
		t.Fatalf("TotalBytes = %d, %v; want %d", n, err, 2*len(data))
	}
}
