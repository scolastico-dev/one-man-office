// Package archtest holds checks on the repository's package structure.
package archtest

import (
	"go/ast"
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
	"claudetrust", "agentcli", "homefile", "childenv", "branchname", "homezip",
	"mcpserver", "aicmd", "mcpauth", "mcprelay", "mcpclients", "mcpbridge", "oldhome",
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

// TestPluginsFolderHoldsNoGoCode keeps the top-level plugins folder a tree
// of plugin files only: omo installs every plugin from a git source, so no
// Go code there embeds plugins into the binary.
func TestPluginsFolderHoldsNoGoCode(t *testing.T) {
	err := filepath.WalkDir(filepath.Join("..", "..", "plugins"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			t.Errorf("%s: plugins are not built into omo; install them from a git source", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestOnlyLuaboxCreatesLuaStates holds luabox's bounds (its gopher-lua
// settings among them) for every Lua state omo creates: no other package
// may create one.
func TestOnlyLuaboxCreatesLuaStates(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
	for _, top := range []string{"internal", "cmd", "plugins"} {
		err := filepath.WalkDir(filepath.Join("..", "..", top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
				filepath.Base(filepath.Dir(path)) == "luabox" {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if p != "github.com/yuin/gopher-lua" {
					continue
				}
				name := "lua"
				if imp.Name != nil {
					name = imp.Name.Name
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if name == "." || strings.Contains(string(raw), name+".NewState(") {
					t.Errorf("%s creates a gopher-lua state; only internal/luabox may", path)
				}
				checked++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked == 0 {
		t.Fatal("no file outside luabox imports gopher-lua; is the walk wrong?")
	}
}

// TestNoBranchNamePrefixRule keeps the shape of a ticket branch out of the
// code: it is whatever the user's branch template gives, so no code outside
// internal/branchname (which holds the default template) may name a branch
// by a literal starting with "omo/", such as a prefix check that decides
// what omo may push or delete. Ownership is the recorded branch
// (engine.checkOwnBranch).
func TestNoBranchNamePrefixRule(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join("..", "..", top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
				filepath.Base(filepath.Dir(path)) == "branchname" {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			checked++
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if v, err := strconv.Unquote(lit.Value); err == nil && strings.HasPrefix(v, "omo/") {
					t.Errorf("%s: the literal %s names a branch by the omo/ prefix; branch names come from the template", fset.Position(lit.Pos()), lit.Value)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked == 0 {
		t.Fatal("no source files were checked")
	}
}
