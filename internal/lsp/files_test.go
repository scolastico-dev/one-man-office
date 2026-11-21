package lsp

import (
	"context"
	"errors"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"testing"
)

func TestFileMappingAndUTF16Ranges(t *testing.T) {
	reg := api.LSPServer{Files: []api.LSPFileSelector{{Pattern: "src/*.go", LanguageID: "special"}, {Pattern: "*.go", LanguageID: "go"}}}
	for _, tt := range []struct{ file, want string }{{"src/a.go", "special"}, {"nested/src/a.go", "go"}, {"a.GO", ""}, {"file.go", "go"}, {"main.txt", ""}} {
		if got := language(reg, tt.file); got != tt.want {
			t.Errorf("%s: %s, want %s", tt.file, got, tt.want)
		}
	}
	for _, tt := range []struct {
		text string
		want Position
	}{{"", Position{0, 0}}, {"😀x", Position{0, 3}}, {"a\n😀x", Position{1, 3}}, {"a\r\n", Position{1, 0}}} {
		if got := endPosition(tt.text); got != tt.want {
			t.Errorf("%q: %+v, want %+v", tt.text, got, tt.want)
		}
	}
}

func TestWatcherGlobs(t *testing.T) {
	for _, tt := range []struct {
		pattern, file string
		want          bool
	}{
		{"**/*.{ts,js}", "src/main.ts", true}, {"**/*.{ts,js}", "main.js", true}, {"**/*.{ts,js}", "main.go", false},
		{"**/[!a]*.go", "src/b.go", true}, {"**/[!a]*.go", "src/a.go", false},
		{"/work/src/*.go", "/work/src/main.go", true}, {"**/*.go", "main.go", true}, {"src/*", "src/a/b", false},
	} {
		if got := glob(tt.pattern, tt.file); got != tt.want {
			t.Errorf("glob(%q,%q)=%v, want %v", tt.pattern, tt.file, got, tt.want)
		}
	}
}

func TestFileScanningHonorsShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanFiles(ctx, t.TempDir(), api.LSPServer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("scan ignored shutdown: %v", err)
	}
}
