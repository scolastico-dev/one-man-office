package lsp

import (
	"context"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

const maxDocument = 4 << 20

type document struct {
	text     string
	language string
	version  int
}

func language(reg api.LSPServer, file string) string {
	for _, f := range reg.Files {
		candidate := file
		if !strings.Contains(f.Pattern, "/") {
			candidate = path.Base(file)
		}
		if ok, _ := path.Match(f.Pattern, candidate); ok {
			return f.LanguageID
		}
	}
	return ""
}
func scanFiles(ctx context.Context, root string, reg api.LSPServer, onSkip func(string, error)) (map[string]document, error) {
	docs := map[string]document{}
	skip := func(name string, err error) {
		if onSkip != nil {
			onSkip(name, err)
		}
	}
	err := filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			skip(name, err)
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			skip(name, err)
			return nil
		}
		rel = filepath.ToSlash(rel)
		lang := language(reg, rel)
		if lang == "" {
			return nil
		}
		data, err := homefile.Read(name, maxDocument)
		if err != nil {
			if !os.IsNotExist(err) {
				skip(rel, err)
			}
			return nil
		}
		docs[rel] = document{text: string(data), language: lang}
		return nil
	})
	return docs, err
}
func fileURI(file string) string {
	p := filepath.ToSlash(file)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// uriFilePath turns a local file URI into a slash-separated file name. The
// path of file:///D:/a/b is "/D:/a/b"; the slash before a drive letter is not
// part of the name.
func uriFilePath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	p := u.Path
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return p, true
}
func relativeFile(root, uri string) (string, bool) {
	p, ok := uriFilePath(uri)
	if !ok {
		return "", false
	}
	abs := filepath.Clean(filepath.FromSlash(p))
	if !within(root, abs) {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	return filepath.ToSlash(rel), err == nil
}
func endPosition(text string) Position {
	lines := strings.Split(text, "\n")
	return Position{len(lines) - 1, len(utf16.Encode([]rune(lines[len(lines)-1])))}
}
