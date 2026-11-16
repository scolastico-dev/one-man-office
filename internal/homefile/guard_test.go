package homefile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// homePackages are the packages that read files in the omo home. Every such
// read goes through Open or Read.
var homePackages = []string{"settings", "serverconfig", "globalhome", "service", "engine", "agents", "server", "session",
	"usage", "plugins", "pluginmanager", "skillsets", "images"}

// notHome are reads in those packages of files outside the omo home, by file
// and call.
var notHome = map[string]string{
	"agents/leftovers_unix.go os.ReadFile": "/proc/<pid>/stat",
	"session/kill_linux.go os.ReadFile":    "/proc/<pid>/environ",
}

// TestHomeFilesReadThroughHomefile is the guard: a file of the home packages
// that opens or reads a file another way fails, unless notHome names it. A
// file opened only for writing (no O_RDONLY or O_RDWR) passes.
func TestHomeFilesReadThroughHomefile(t *testing.T) {
	for _, pkg := range homePackages {
		dir := filepath.Join("..", pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				if imp.Path.Value == `"io/ioutil"` {
					t.Errorf("%s/%s imports io/ioutil", pkg, name)
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || id.Name != "os" {
					return true
				}
				call1 := "os." + sel.Sel.Name
				switch sel.Sel.Name {
				case "ReadFile", "Open":
				case "OpenFile":
					if len(call.Args) < 2 || !opensForReading(call.Args[1]) {
						return true
					}
				default:
					return true
				}
				key := pkg + "/" + name + " " + call1
				if _, ok := notHome[key]; !ok {
					t.Errorf("%s: %s reads a file without homefile.Open or homefile.Read", fset.Position(call.Pos()), call1)
				}
				return true
			})
		}
	}
}

// opensForReading reports whether the flags of os.OpenFile may open for
// reading: O_RDONLY or O_RDWR named, or no O_WRONLY.
func opensForReading(flags ast.Expr) bool {
	var names []string
	ast.Inspect(flags, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			names = append(names, sel.Sel.Name)
		}
		return true
	})
	write := false
	for _, n := range names {
		switch n {
		case "O_RDONLY", "O_RDWR":
			return true
		case "O_WRONLY":
			write = true
		}
	}
	return !write
}
