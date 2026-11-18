package boundedzip

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// All production ZIP constructors must preflight metadata before archive/zip
// parses and allocates its entry list. Tests may construct arbitrary fixtures.
func TestNoUnboundedZIPConstructors(t *testing.T) {
	err := filepath.WalkDir("..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || filepath.Dir(p) == "../boundedzip" {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			return err
		}
		alias := ""
		for _, imp := range f.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			if name == "archive/zip" {
				alias = "zip"
				if imp.Name != nil {
					alias = imp.Name.Name
				}
			}
		}
		if alias == "" {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			s, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := s.X.(*ast.Ident)
			if ok && id.Name == alias && (s.Sel.Name == "NewReader" || s.Sel.Name == "OpenReader") {
				t.Errorf("%s parses ZIP without boundedzip preflight", p)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
