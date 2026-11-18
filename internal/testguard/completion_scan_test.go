package testguard

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

// completionGuesses finds the two guesses a test must not make about work
// that runs on another goroutine:
//
//   - runtime.NumGoroutine anywhere: the count is global, so unrelated runtime
//     or test goroutines make a correct run fail and the exit of an unrelated
//     goroutine hides a leak of the code under test. Observe the owned work
//     itself (a join, a completion channel, a synctest bubble that ends only
//     when every goroutine it started has ended).
//   - runtime.Gosched in a loop with a fixed bound or none, and a fixed-count
//     loop around a select with a default clause: "nothing happened after N
//     yields" is a negative observation that depends on how the scheduler runs
//     the other goroutine, so it can miss the very bug it names. Observe the
//     exact boundary instead (synctest.Wait, a held channel). The one allowed
//     use is a yield inside a loop that polls a real positive condition
//     (`for !cond() { ... runtime.Gosched() }`).
func completionGuesses(raw []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", raw, 0)
	if err != nil {
		return nil, err
	}
	runtimeName := ""
	for _, spec := range file.Imports {
		if name, _ := strconv.Unquote(spec.Path.Value); name == "runtime" {
			runtimeName = "runtime"
			if spec.Name != nil {
				runtimeName = spec.Name.Name
			}
		}
	}
	runtimeCall := func(n ast.Node, name string) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || runtimeName == "" {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == runtimeName && sel.Sel.Name == name
	}
	// fixedBound reports a loop that runs a fixed number of times or forever.
	fixedBound := func(loop ast.Node) bool {
		switch l := loop.(type) {
		case *ast.RangeStmt:
			return true // a range over a count, a slice or a channel is not a condition
		case *ast.ForStmt:
			if l.Cond == nil {
				return true
			}
			bin, ok := l.Cond.(*ast.BinaryExpr)
			if !ok {
				return false
			}
			for _, side := range []ast.Expr{bin.X, bin.Y} {
				if lit, ok := side.(*ast.BasicLit); ok && lit.Kind == token.INT {
					return true
				}
			}
		}
		return false
	}
	// counted reports a loop over a literal count: `for range 2000`, `for i
	// := 0; i < 2000; i++`.
	counted := func(loop ast.Node) bool {
		switch l := loop.(type) {
		case *ast.RangeStmt:
			lit, ok := l.X.(*ast.BasicLit)
			return ok && lit.Kind == token.INT
		case *ast.ForStmt:
			return l.Cond != nil && fixedBound(loop)
		}
		return false
	}
	hasSelectDefault := func(body ast.Node) bool {
		found := false
		ast.Inspect(body, func(n ast.Node) bool {
			// Another goroutine's body, or a loop that ends on a real signal
			// (a worker of a race stress), is not this loop's interval.
			switch n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.ForStmt, *ast.RangeStmt:
				if n != body && !counted(n) {
					return false
				}
			}
			if sel, ok := n.(*ast.SelectStmt); ok {
				for _, c := range sel.Body.List {
					if cc, ok := c.(*ast.CommClause); ok && cc.Comm == nil {
						found = true
					}
				}
			}
			return !found
		})
		return found
	}
	var problems []string
	var loops []ast.Node
	var walk func(n ast.Node)
	walk = func(root ast.Node) {
		ast.Inspect(root, func(n ast.Node) bool {
			if n == nil {
				return false
			}
			switch n.(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				if n == root {
					return true
				}
				loops = append(loops, n)
				walk(n)
				loops = loops[:len(loops)-1]
				return false
			}
			line := fset.Position(n.Pos()).Line
			switch {
			case runtimeCall(n, "NumGoroutine"):
				problems = append(problems, fmt.Sprintf("%d: runtime.NumGoroutine is a global count; observe the owned work (a join, a completion channel or a synctest bubble)", line))
			case runtimeCall(n, "Gosched"):
				if len(loops) == 0 || fixedBound(loops[len(loops)-1]) {
					problems = append(problems, fmt.Sprintf("%d: runtime.Gosched outside a loop that polls a positive condition; a fixed number of yields is not a completion boundary", line))
				}
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		walk(decl)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch l := n.(type) {
		case *ast.ForStmt:
			body = l.Body
		case *ast.RangeStmt:
			body = l.Body
		default:
			return true
		}
		if counted(n) && hasSelectDefault(body) {
			problems = append(problems, fmt.Sprintf("%d: fixed-count loop around a select with default is a negative observation over a guessed interval; observe the exact boundary", fset.Position(n.Pos()).Line))
		}
		return true
	})
	return problems, nil
}

func TestCompletionGuessGuardRecognizesGuesses(t *testing.T) {
	for name, body := range map[string]string{
		"global count":      `n := runtime.NumGoroutine(); _ = n`,
		"yield counter":     `for i := 0; i < 2000; i++ { runtime.Gosched() }`,
		"yield range":       `for range 2000 { runtime.Gosched() }`,
		"yield forever":     `for { runtime.Gosched() }`,
		"quiet window":      `for range 2000 { select { case <-done: t.Fatal("early"); default: runtime.Gosched() } }`,
		"quiet window slow": `for i := 0; i < 50; i++ { select { case <-done: t.Fatal("early"); default: time.Sleep(time.Millisecond) } }`,
		"bare yield":        `runtime.Gosched()`,
	} {
		t.Run(name, func(t *testing.T) {
			src := `package sample; import "runtime"; import "time"; func TestCase(t *testing.T) { var done chan struct{}; _ = time.Second; ` + body + ` }`
			problems, err := completionGuesses([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) == 0 {
				t.Fatal("not recognized")
			}
		})
	}
	t.Run("aliased import", func(t *testing.T) {
		problems, err := completionGuesses([]byte(`package sample; import rt "runtime"; func f() { _ = rt.NumGoroutine() }`))
		if err != nil || len(problems) != 1 {
			t.Fatalf("got %v, %v", problems, err)
		}
	})
}

func TestCompletionGuessGuardAllowsPositivePolling(t *testing.T) {
	for name, body := range map[string]string{
		"condition poll":      `for !cond() { runtime.Gosched() }`,
		"condition and limit": `for !cond() && n < limit { runtime.Gosched() }`,
		"counted work":        `for i := 0; i < 100; i++ { work(i) }`,
		"select without loop": `select { case <-done: default: }`,
		"blocking select":     `for range 100 { select { case <-done: case <-other: } }`,
	} {
		t.Run(name, func(t *testing.T) {
			src := `package sample; import "runtime"; func f() { var done, other chan struct{}; var n, limit int; _ = n; _ = limit; cond := func() bool { return true }; work := func(int) {}; _, _, _ = done, other, work; ` + body + ` }`
			problems, err := completionGuesses([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != 0 {
				t.Fatalf("got %v", problems)
			}
		})
	}
}

// Every Go file of the repository, tests and product code, is checked. A
// positive polling loop (`for !cond() { runtime.Gosched() }`) stays allowed.
func TestRepositoryHasNoCompletionGuesses(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "bin", "node_modules", "dist", "superpowers":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		problems, err := completionGuesses(raw)
		if err != nil {
			return err
		}
		checked++
		for _, problem := range problems {
			t.Errorf("%s:%s", path, problem)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 300 {
		t.Fatalf("checked only %d Go files", checked)
	}
}
