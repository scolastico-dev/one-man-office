package testguard

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"strconv"
	"time"
)

// timingProblems intentionally does not infer performance from arbitrary names,
// forbid polling watchdogs or maintain per-file exemptions. It recognizes real
// elapsed expressions and constant duration arithmetic, including local aliases.
func timingProblems(raw []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", raw, 0)
	if err != nil {
		return nil, err
	}
	imports := map[string]string{}
	for _, spec := range file.Imports {
		name, _ := strconv.Unquote(spec.Path.Value)
		if name != "time" && name != "testing/synctest" {
			continue
		}
		alias := "time"
		if name == "testing/synctest" {
			alias = "synctest"
		}
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		imports[alias] = name
	}
	isCall := func(expr ast.Expr, pkg, name string) bool {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && imports[id.Name] == pkg && sel.Sel.Name == name
	}
	globals := map[string]ast.Expr{}
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range gen.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok {
					for i, name := range value.Names {
						if i < len(value.Values) {
							globals[name.Name] = value.Values[i]
						}
					}
				}
			}
		}
	}
	var problems []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		bindings := map[string]ast.Expr{}
		for k, v := range globals {
			bindings[k] = v
		}
		var elapsed func(ast.Expr, int) bool
		elapsed = func(expr ast.Expr, depth int) bool {
			if depth > 20 {
				return false
			}
			if isCall(expr, "time", "Since") {
				return true
			}
			switch e := expr.(type) {
			case *ast.ParenExpr:
				return elapsed(e.X, depth+1)
			case *ast.Ident:
				return elapsed(bindings[e.Name], depth+1)
			case *ast.CallExpr:
				sel, ok := e.Fun.(*ast.SelectorExpr)
				return ok && sel.Sel.Name == "Sub" && isCall(sel.X, "time", "Now")
			}
			return false
		}
		var duration func(ast.Expr, int) constant.Value
		duration = func(expr ast.Expr, depth int) constant.Value {
			if depth > 20 {
				return constant.MakeUnknown()
			}
			switch e := expr.(type) {
			case *ast.BasicLit:
				return constant.MakeFromLiteral(e.Value, e.Kind, 0)
			case *ast.ParenExpr:
				return duration(e.X, depth+1)
			case *ast.Ident:
				return duration(bindings[e.Name], depth+1)
			case *ast.SelectorExpr:
				id, ok := e.X.(*ast.Ident)
				if ok && imports[id.Name] == "time" {
					values := map[string]time.Duration{"Nanosecond": time.Nanosecond, "Microsecond": time.Microsecond,
						"Millisecond": time.Millisecond, "Second": time.Second, "Minute": time.Minute, "Hour": time.Hour}
					if value, ok := values[e.Sel.Name]; ok {
						return constant.MakeInt64(int64(value))
					}
				}
			case *ast.BinaryExpr:
				left, right := duration(e.X, depth+1), duration(e.Y, depth+1)
				if left.Kind() == constant.Unknown || right.Kind() == constant.Unknown {
					break
				}
				switch e.Op {
				case token.ADD, token.SUB, token.MUL:
					return constant.BinaryOp(left, e.Op, right)
				case token.QUO:
					if constant.Sign(right) != 0 {
						return constant.BinaryOp(left, e.Op, right)
					}
				}
			}
			return constant.MakeUnknown()
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			// Virtual clocks do not measure runner performance.
			if call, ok := node.(*ast.CallExpr); ok && isCall(call, "testing/synctest", "Test") {
				return false
			}
			switch n := node.(type) {
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if name, ok := lhs.(*ast.Ident); ok && i < len(n.Rhs) {
						bindings[name.Name] = n.Rhs[i]
					}
				}
			case *ast.ValueSpec:
				for i, name := range n.Names {
					if i < len(n.Values) {
						bindings[name.Name] = n.Values[i]
					}
				}
			case *ast.BinaryExpr:
				var limit ast.Expr
				if (n.Op == token.GTR || n.Op == token.GEQ) && elapsed(n.X, 0) {
					limit = n.Y
				}
				if (n.Op == token.LSS || n.Op == token.LEQ) && elapsed(n.Y, 0) {
					limit = n.X
				}
				value := duration(limit, 0)
				if value.Kind() != constant.Unknown && constant.Sign(value) >= 0 &&
					constant.Compare(value, token.LSS, constant.MakeInt64(int64(time.Minute))) {
					problems = append(problems, fmt.Sprintf("%d: short elapsed upper bound; wait for completion or use a documented generous watchdog", fset.Position(n.Pos()).Line))
				}
			}
			return true
		})
	}
	return problems, nil
}
