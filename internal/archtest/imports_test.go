// Package archtest holds checks on the repository's package structure.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/scolastico-dev/one-man-office/"

// testSupport are packages for tests only: test files and the browser
// tests' helper programs below web/e2e import them, the product never does.
var testSupport = []string{
	"internal/fakeagent", "internal/mcprelay/fakemcp", "internal/gittest",
	"internal/testpath", "internal/testport", "internal/childenv/childenvtest",
}

// importedOnlyBy names, for packages at the top of the layering, the only
// packages that may import them: the command line sits on the server, the
// server on the engine; nothing below them reaches up. oldhome (the upgrade
// from the earlier omo) belongs to the command line.
var importedOnlyBy = map[string][]string{
	"internal/cli":     {"cmd/omo"},
	"internal/server":  {"internal/cli"},
	"internal/engine":  {"internal/server"},
	"internal/oldhome": {"internal/cli"},
}

// leaves import no package of the module: api holds the wire types every
// side shares.
var leaves = []string{"internal/api", "internal/textsafe", "internal/homefile", "internal/childenv"}

// removed are packages of the earlier office-based design; none may come
// back under its old name.
var removed = []string{
	"config", "modelusage", "yamlformat", "pluginfiles",
	"office", "supervisor", "tui", "bus", "queue", "proto", "sockc", "sockd",
	"company", "companyservice",
}

// importGraph reads the imports of the module's own packages from the
// non-test Go files below internal, cmd and web, keyed by the package's
// folder relative to the repository ("internal/server").
func importGraph(t *testing.T) map[string][]string {
	t.Helper()
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	graph := map[string][]string{}
	for _, top := range []string{"internal", "cmd", "web"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			pkg := filepath.ToSlash(rel)
			if _, ok := graph[pkg]; !ok {
				graph[pkg] = nil
			}
			for _, spec := range file.Imports {
				imported, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if own, ok := strings.CutPrefix(imported, module); ok && !slices.Contains(graph[pkg], own) {
					graph[pkg] = append(graph[pkg], own)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return graph
}

// boundaryViolations lists every import in graph that crosses a boundary.
func boundaryViolations(graph map[string][]string) []string {
	var out []string
	for pkg, imports := range graph {
		for _, imp := range imports {
			if slices.Contains(testSupport, imp) && !strings.HasPrefix(pkg, "web/e2e/") && !slices.Contains(testSupport, pkg) {
				out = append(out, pkg+" imports "+imp+", which is for tests only")
			}
			if allowed, ok := importedOnlyBy[imp]; ok && !slices.Contains(allowed, pkg) {
				out = append(out, pkg+" imports "+imp+", which only "+strings.Join(allowed, ", ")+" may import")
			}
			if slices.Contains(leaves, pkg) {
				out = append(out, pkg+" imports "+imp+"; it imports no package of omo")
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestPackageBoundaries(t *testing.T) {
	graph := importGraph(t)
	for _, pkg := range []string{"cmd/omo", "internal/cli", "internal/server", "internal/engine", "internal/api"} {
		if _, ok := graph[pkg]; !ok {
			t.Fatalf("%s was not read; is the walk wrong?", pkg)
		}
	}
	for _, v := range boundaryViolations(graph) {
		t.Error(v)
	}
}

// TestBoundaryViolationsAreFound keeps the guard able to fail.
func TestBoundaryViolationsAreFound(t *testing.T) {
	got := boundaryViolations(map[string][]string{
		"cmd/omo":                {"internal/cli"},
		"internal/cli":           {"internal/server", "internal/oldhome"},
		"internal/server":        {"internal/engine", "internal/fakeagent"},
		"internal/board":         {"internal/engine", "internal/api"},
		"internal/api":           {"internal/textsafe"},
		"internal/settings":      {"internal/cli", "internal/gittest"},
		"web/e2e/fakeagent":      {"internal/fakeagent"},
		"internal/fakeagent":     {"internal/api"},
		"internal/engine":        {"internal/oldhome"},
		"internal/mcprelay":      {"internal/mcprelay/fakemcp"},
		"internal/childenv":      nil,
		"internal/childenv/test": {"internal/childenv/childenvtest"},
	})
	want := []string{
		"internal/api imports internal/textsafe; it imports no package of omo",
		"internal/board imports internal/engine, which only internal/server may import",
		"internal/childenv/test imports internal/childenv/childenvtest, which is for tests only",
		"internal/engine imports internal/oldhome, which only internal/cli may import",
		"internal/mcprelay imports internal/mcprelay/fakemcp, which is for tests only",
		"internal/server imports internal/fakeagent, which is for tests only",
		"internal/settings imports internal/cli, which only cmd/omo may import",
		"internal/settings imports internal/gittest, which is for tests only",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("violations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestRemovedPackagesStayRemoved fails when a package of the earlier design
// comes back under its old name.
func TestRemovedPackagesStayRemoved(t *testing.T) {
	for _, name := range removed {
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
