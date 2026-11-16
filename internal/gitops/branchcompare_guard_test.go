package gitops

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Branch names are compared folded (branchname.Same, Below, In) wherever omo
// decides whether a name is taken, protected, recorded, checked out or a
// ticket's own: on macOS and Windows git keeps Fix and fix in one file.
// This guard fails on an exact comparison of a branch name in the packages
// that make those decisions. A comparison that must stay exact says why in
// a comment holding "exact:" on its line or the line before.
func TestBranchNamesAreComparedFolded(t *testing.T) {
	for _, dir := range []string{".", "../engine"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, finding := range exactBranchCompares(t, path, string(src)) {
				t.Errorf("%s; compare branch names with branchname.Same, Below or In (or mark it exact: with the reason)", finding)
			}
		}
	}
}

// The guard finds the comparisons it is there for.
func TestBranchCompareGuardFinds(t *testing.T) {
	src := `package x
func f() {
	_ = branch == name
	_ = wt.branch != "refs/heads/" + b
	_ = strings.HasPrefix(r, name+"/")
	_ = slices.Contains(protected, name)
	// exact: allowed
	_ = branch == p.Branch
	_ = branch == ""
}`
	if got := exactBranchCompares(t, "x.go", src); len(got) != 4 {
		t.Fatalf("found %d, want 4: %q", len(got), got)
	}
}

// exactBranchCompares lists the exact comparisons of branch names in the Go
// source src.
func exactBranchCompares(t *testing.T, path, src string) []string {
	branchy := map[string]bool{"branch": true, "Branch": true, "base": true, "Base": true, "target": true, "name": true}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(src, "\n")
	var out []string
	report := func(n ast.Node, what string) {
		line := fset.Position(n.Pos()).Line
		if strings.Contains(lines[line-1], "exact:") || line >= 2 && strings.Contains(lines[line-2], "exact:") {
			return
		}
		out = append(out, fmt.Sprintf("%s: %s", fset.Position(n.Pos()), what))
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op != token.EQL && x.Op != token.NEQ || isEmptyLit(x.X) || isEmptyLit(x.Y) || isNil(x.X) || isNil(x.Y) {
				return true
			}
			for _, side := range []ast.Expr{x.X, x.Y} {
				if headsConcat(side) {
					report(x, `exact comparison with "refs/heads/"+name`)
					return true
				}
			}
			for _, side := range []ast.Expr{x.X, x.Y} {
				if name := identName(side); branchy[name] {
					report(x, "exact comparison of "+name)
					return true
				}
			}
		case *ast.CallExpr:
			switch fn := callName(x); fn {
			case "strings.HasPrefix":
				if len(x.Args) == 2 && endsWithSlash(x.Args[1]) && (branchy[identName(x.Args[0])] || branchy[identName(x.Args[1].(*ast.BinaryExpr).X)]) {
					report(x, `strings.HasPrefix(..., name+"/")`)
				}
			case "slices.Contains", "slices.Index":
				if len(x.Args) == 2 {
					if name := strings.ToLower(identName(x.Args[0])); strings.Contains(name, "protected") || strings.Contains(name, "recorded") {
						report(x, fn+" on "+name)
					}
				}
			}
		}
		return true
	})
	return out
}

func headsConcat(e ast.Expr) bool {
	b, ok := e.(*ast.BinaryExpr)
	if !ok || b.Op != token.ADD {
		return false
	}
	for {
		l, ok := b.X.(*ast.BinaryExpr)
		if !ok || l.Op != token.ADD {
			break
		}
		b = l
	}
	lit, ok := b.X.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	s, _ := strconv.Unquote(lit.Value)
	return s == "refs/heads/"
}

func endsWithSlash(e ast.Expr) bool {
	b, ok := e.(*ast.BinaryExpr)
	if !ok || b.Op != token.ADD {
		return false
	}
	lit, ok := b.Y.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	s, _ := strconv.Unquote(lit.Value)
	return s == "/"
}

func identName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

func isEmptyLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Value == `""`
}

func isNil(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

func callName(c *ast.CallExpr) string {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}
