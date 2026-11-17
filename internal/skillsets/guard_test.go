package skillsets

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSkillSetsReachAgentsOnlyThroughText guards the one filter: outside
// the settings store and this package, the list of skill sets is read only
// as an argument of skillsets.Text (which applies ForRole and the paths),
// or by the routes that show the settings themselves. A new place that
// hands skill sets to an agent session (another program, another role)
// has to go through Text too, or be added here on purpose.
func TestSkillSetsReachAgentsOnlyThroughText(t *testing.T) {
	root := filepath.Join("..", "..")
	// file -> functions that may read the list for something other than
	// an agent's prompt.
	allowed := map[string][]string{
		filepath.Join("internal", "server", "skillsets.go"): {"listSkillSets"},
	}
	var found int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch rel {
			case "web", ".git", filepath.Join("internal", "settings"), filepath.Join("internal", "skillsets"):
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(src), "SkillSets()") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			okFn := false
			for _, name := range allowed[rel] {
				okFn = okFn || name == fn.Name.Name
			}
			inText := map[ast.Node]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if isSel(call.Fun, "skillsets", "Text") {
					for _, arg := range call.Args {
						inText[arg] = true
					}
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "SkillSets" && len(call.Args) == 0 {
					found++
					if !inText[call] && !okFn {
						t.Errorf("%s: %s reads the skill sets outside skillsets.Text; agents must get skill sets only through Text", rel, fn.Name.Name)
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found < 3 {
		t.Fatalf("found %d reads of the skill sets; the guard no longer sees the code", found)
	}
}

func isSel(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}
