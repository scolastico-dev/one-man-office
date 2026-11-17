package web

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestFilesFromReportsMissingBuild(t *testing.T) {
	if _, ok := filesFrom(fstest.MapFS{"dist/.gitkeep": {}}); ok {
		t.Fatal("ok = true without an index.html")
	}
}

func TestFilesFromServesBuiltApp(t *testing.T) {
	sub, ok := filesFrom(fstest.MapFS{
		"dist/index.html":    {Data: []byte("<!doctype html>")},
		"dist/assets/a-1.js": {Data: []byte("x")},
	})
	if !ok {
		t.Fatal("ok = false with an index.html")
	}
	b, err := fs.ReadFile(sub, "assets/a-1.js")
	if err != nil || string(b) != "x" {
		t.Fatalf("read asset: %q, %v", b, err)
	}
}
