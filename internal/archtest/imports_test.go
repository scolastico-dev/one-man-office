// Package archtest holds checks on the repository's package structure.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/scolastico-dev/one-man-office/"

// guarded are the packages of the current design. They must not depend on
// the frozen configuration package, the usage helpers or anything that was
// removed with the earlier office-based design.
var guarded = []string{
	"api", "board", "server", "settings", "serverconfig", "service", "webterm",
	"transport", "apiclient", "cli", "globalhome", "db",
	"engine", "agents", "prompts", "fakeagent", "gitops", "names", "session",
	"usage", "luabox", "skillsets", "forge", "plugins", "pluginmanager",
	"codexauth", "discover", "agentsetup", "folderlist", "textsafe", "userterm",
	"claudetrust", "agentcli", "homefile",
}

var forbidden = []string{
	"config", "modelusage", "yamlformat", "pluginfiles",
	"office", "supervisor", "tui", "bus", "queue", "proto", "sockc", "sockd",
	"company", "companyservice",
}

func forbiddenImport(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, module+"internal/")
	if !ok {
		return "", false
	}
	for _, name := range forbidden {
		if rest == name || strings.HasPrefix(rest, name+"/") {
			return name, true
		}
	}
	return "", false
}

func TestNewPackagesDoNotImportRemovedOrFrozenPackages(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
	for _, pkg := range guarded {
		dir := filepath.Join("..", pkg)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			checked++
			for _, spec := range file.Imports {
				imported, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if name, bad := forbiddenImport(imported); bad {
					t.Errorf("%s imports internal/%s, which is frozen or removed", filepath.ToSlash(path), name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan internal/%s: %v", pkg, err)
		}
	}
	if checked == 0 {
		t.Fatal("no source files were checked")
	}
}

func TestForbiddenImportMatching(t *testing.T) {
	for path, want := range map[string]bool{
		module + "internal/config":         true,
		module + "internal/company/assets": true,
		module + "internal/companyservice": true,
		module + "internal/configuration":  false,
		module + "internal/service":        false,
		module + "internal/modelusage":     true,
		"github.com/spf13/cobra":           false,
		"example.com/x/internal/config":    false,
		module + "internal/transport":      false,
	} {
		if _, got := forbiddenImport(path); got != want {
			t.Errorf("forbiddenImport(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestRemovedPackagesStayRemoved fails when a package of the removed
// configuration or usage code comes back under its old name.
func TestRemovedPackagesStayRemoved(t *testing.T) {
	for _, name := range []string{"config", "modelusage", "yamlformat", "pluginfiles"} {
		if _, err := os.Stat(filepath.Join("..", name)); err == nil {
			t.Errorf("internal/%s exists; it was removed", name)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}
