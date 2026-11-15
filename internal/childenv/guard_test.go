package childenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// exempt are the files not checked, with the reason.
var exempt = map[string]string{
	// omo starting itself in the background: the server is omo, not a
	// program started on the user's behalf; what it starts is cleaned.
	"internal/service/service.go": "omo itself",
	// The scripted stand-in for an agent CLI, used only by tests.
	"internal/fakeagent/ops.go": "test stand-in",
}

// TestChildrenGetTheirEnvironmentFromChildenv fails when a program omo
// starts could get omo's environment past Inherited: os.Environ or
// syscall.Environ outside this package, an exec.Command or
// exec.CommandContext in a function that does not set the command's Env, or
// a conpty.Start without conpty.ConPtyEnv.
func TestChildrenGetTheirEnvironmentFromChildenv(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	checked := 0
	for _, top := range []string{"internal", "cmd", "plugins"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/childenv/") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			checked++
			checkFile(t, fset, rel, file)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < 50 {
		t.Fatalf("only %d files checked; is the walk wrong?", checked)
	}
}

func checkFile(t *testing.T, fset *token.FileSet, rel string, file *ast.File) {
	t.Helper()
	if _, ok := exempt[rel]; ok {
		return
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			switch callName(call) {
			case "os.Environ", "syscall.Environ":
				t.Errorf("%s: %s; use childenv.Inherited (or childenv.Own to read a value)", fset.Position(call.Pos()), callName(call))
			case "conpty.Start":
				if !hasArgCall(call, "conpty.ConPtyEnv") {
					t.Errorf("%s: conpty.Start without conpty.ConPtyEnv inherits omo's environment", fset.Position(call.Pos()))
				}
			}
		}
		return true
	})
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var starts []*ast.CallExpr
		setsEnv := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if name := callName(n); name == "exec.Command" || name == "exec.CommandContext" {
					starts = append(starts, n)
				}
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Env" {
						setsEnv = true
					}
				}
			}
			return true
		})
		if len(starts) > 0 && !setsEnv {
			t.Errorf("%s: %s starts a program without setting its Env; take it from childenv.Inherited", fset.Position(starts[0].Pos()), fn.Name.Name)
		}
	}
}

func callName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

func hasArgCall(call *ast.CallExpr, name string) bool {
	for _, arg := range call.Args {
		if c, ok := arg.(*ast.CallExpr); ok && callName(c) == name {
			return true
		}
	}
	return false
}
