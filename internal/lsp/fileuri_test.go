package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A file URI's path keeps a leading slash before a Windows drive letter
// (file:///D:/a/b). Every conversion from a URI to a file name must go
// through uriFilePath so that slash never reaches the file system.
func TestURIFilePathDropsSlashBeforeDriveLetter(t *testing.T) {
	for _, tc := range []struct{ uri, want string }{
		{"file:///D:/a/one-man-office/main.test", "D:/a/one-man-office/main.test"},
		{"file:///c:/x", "c:/x"},
		{"file:///home/user/main.test", "/home/user/main.test"},
		{"file://localhost/home/user/a%20b.test", "/home/user/a b.test"},
	} {
		got, ok := uriFilePath(tc.uri)
		if !ok || got != tc.want {
			t.Errorf("uriFilePath(%q) = %q, %v; want %q", tc.uri, got, ok, tc.want)
		}
	}
	for _, bad := range []string{"http://x/y", "file://remote/x", "file:///x?q=1", "file:///x#f", "::"} {
		if got, ok := uriFilePath(bad); ok {
			t.Errorf("uriFilePath(%q) accepted: %q", bad, got)
		}
	}
}

// Guard: no file in this package may read a file URI's Path directly; it
// would keep the slash before a drive letter on Windows.
func TestNoRawURIPathConversion(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if f == "files.go" || f == "fileuri_test.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(src, []byte("url.Parse(")) {
			t.Errorf("%s parses a URL itself; use uriFilePath or relativeFile", f)
		}
	}
}
