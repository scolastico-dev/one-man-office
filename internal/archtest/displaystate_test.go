package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EndReason is presentation only. The engine and plugin classification use
// EndKind; the sole legacy sentinel conversion belongs to a DB migration.
func TestRuntimeStateDoesNotReadSessionEndMessages(t *testing.T) {
	for _, dir := range []string{"../engine", "../plugins", "../server"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == "EndReason" {
					t.Errorf("%s reads EndReason: runtime state must use EndKind", path)
				}
				return true
			})
		}
	}
}

func TestAttentionIsNotParsedForState(t *testing.T) {
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if cmp, ok := n.(*ast.BinaryExpr); ok && (cmp.Op == token.EQL || cmp.Op == token.NEQ) {
				for _, side := range []ast.Expr{cmp.X, cmp.Y} {
					if call, ok := side.(*ast.CallExpr); ok {
						if fn, ok := call.Fun.(*ast.Ident); ok && (fn.Name == "noModelText" || fn.Name == "projectText") {
							t.Errorf("%s compares planner display words for state: use AttentionOwner", path)
						}
					}
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := fn.X.(*ast.Ident)
			if !ok || (pkg.Name != "strings" && pkg.Name != "regexp") {
				return true
			}
			for _, arg := range call.Args {
				ast.Inspect(arg, func(n ast.Node) bool {
					if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == "Attention" {
						t.Errorf("%s parses Attention with %s: use structured state", path, fn.Sel.Name)
					}
					return true
				})
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
