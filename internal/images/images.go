// Package images keeps the images people and agents upload to tickets: what
// is accepted (sniffed from the content, never from a name or a declared
// type), how the files are stored in the images folder of the omo home
// (under a random UUID, never under a name from the request), how they are
// read back (no link followed, regular files only, bounded) and how a
// Markdown reference to one looks and is rewritten for agents and exports.
package images

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"  // registers the GIF header reader for DecodeConfig
	_ "image/jpeg" // registers the JPEG header reader for DecodeConfig
	_ "image/png"  // registers the PNG header reader for DecodeConfig
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Limits. They bound what an agent (or anyone) can put on the disk through
// uploads: MaxBytes per image, MaxPerTicket stored images per ticket and
// MaxTotalBytes over all stored images. Expired images count for none.
const (
	MaxBytes      = 10 << 20
	MaxPerTicket  = 100
	MaxTotalBytes = 2 << 30
	// MaxSide bounds the width and the height in pixels, so a small file
	// cannot make the browser decode a giant picture.
	MaxSide = 16384
	// MaxNameLength bounds the kept original name, in characters.
	MaxNameLength = 200
)

// The accepted types. SVG is refused: it is a document that can carry
// scripts, not a picture.
const (
	TypePNG  = "image/png"
	TypeJPEG = "image/jpeg"
	TypeGIF  = "image/gif"
	TypeWebP = "image/webp"
)

// Extension returns the file extension omo stores a type under, without
// the dot, or "" for a type omo does not accept.
func Extension(contentType string) string {
	switch contentType {
	case TypePNG:
		return "png"
	case TypeJPEG:
		return "jpg"
	case TypeGIF:
		return "gif"
	case TypeWebP:
		return "webp"
	}
	return ""
}

// Refusal is the error of content omo does not take as an image. Its text is
// a sentence for the person or agent who uploaded it.
type Refusal string

func (r Refusal) Error() string { return string(r) }

// Sniff decides from the first bytes and the header of data what it is. It
// returns the content type for PNG, JPEG, GIF and WebP whose header reads
// as a picture of at most MaxSide pixels a side, and a Refusal for anything
// else (SVG included).
func Sniff(data []byte) (string, error) {
	notImage := Refusal("this file is not a PNG, JPEG, GIF or WebP image; omo takes only those (SVG is refused, it can carry scripts)")
	var ct string
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		ct = TypePNG
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		ct = TypeJPEG
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		ct = TypeGIF
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		ct = TypeWebP
	default:
		return "", notImage
	}
	var w, h int
	if ct == TypeWebP {
		var ok bool
		if w, h, ok = webpSize(data); !ok {
			return "", notImage
		}
	} else {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", notImage
		}
		w, h = cfg.Width, cfg.Height
	}
	if w < 1 || h < 1 {
		return "", notImage
	}
	if w > MaxSide || h > MaxSide {
		return "", Refusal(fmt.Sprintf("this image is %d×%d pixels; omo takes at most %d pixels a side", w, h, MaxSide))
	}
	return ct, nil
}

// webpSize reads the canvas size of a WebP file from its first chunk.
func webpSize(data []byte) (w, h int, ok bool) {
	if len(data) < 30 {
		return 0, 0, false
	}
	switch string(data[12:16]) {
	case "VP8X":
		w = int(uint32(data[24])|uint32(data[25])<<8|uint32(data[26])<<16) + 1
		h = int(uint32(data[27])|uint32(data[28])<<8|uint32(data[29])<<16) + 1
		return w, h, true
	case "VP8 ":
		// Frame tag (3 bytes), start code 9d 01 2a, then 14-bit sizes.
		if data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, false
		}
		w = int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff)
		h = int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
		return w, h, true
	case "VP8L":
		if data[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		w = int(bits&0x3fff) + 1
		h = int((bits>>14)&0x3fff) + 1
		return w, h, true
	}
	return 0, 0, false
}

// NewID returns a random (version 4) UUID in lower case.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("images: no randomness: %v", err))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidID reports whether s is a UUID as NewID writes it. Only such ids are
// looked up; nothing else from a request is ever used for a file name.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// CleanName returns the name to keep for an upload called raw: only the
// last element of a path, without hidden characters (control, invisible and
// direction characters become spaces), trimmed, at most MaxNameLength
// characters. Without anything left it is "image.<ext>".
func CleanName(raw, contentType string) string {
	if !utf8.ValidString(raw) {
		raw = strings.ToValidUTF8(raw, "")
	}
	raw = strings.ReplaceAll(raw, `\`, "/")
	raw = path.Base("/" + raw)
	raw = strings.Map(func(r rune) rune {
		if textsafe.IsHidden(r) {
			return ' '
		}
		return r
	}, raw)
	raw = strings.Join(strings.Fields(raw), " ")
	if utf8.RuneCountInString(raw) > MaxNameLength {
		raw = strings.TrimSpace(string([]rune(raw)[:MaxNameLength]))
	}
	if raw == "" || raw == "/" || raw == "." || raw == ".." {
		return "image." + Extension(contentType)
	}
	return raw
}

// FileName is the name of an image's file in the images folder.
func FileName(id, contentType string) string { return id + "." + Extension(contentType) }

// Markdown is the reference to an image as written into a description or a
// comment: ![<name>](omo-image:<id>), the name escaped so that it stays the
// alt text whatever it holds.
func Markdown(name, id string) string {
	return "![" + escapeAlt(name) + "](omo-image:" + id + ")"
}

func escapeAlt(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '[', ']', '`', '*', '_', '<', '>':
			b.WriteByte('\\')
		case '\n', '\r', '\t':
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}
