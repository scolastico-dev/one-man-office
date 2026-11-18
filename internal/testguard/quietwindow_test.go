package testguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// quietWindows finds the ways a test turns a short clock into a negative
// observation, "nothing happened within N ms":
//
//   - a short one-shot timer (`time.After(20 * time.Millisecond)`,
//     `time.NewTimer`) in any form: in a select case, or held in a variable and
//     read later (`timer := time.NewTimer(...)`; `case <-timer.C`). A ticker
//     drives a polling loop and is not flagged;
//   - a short `time.Sleep` that does not end the body of a loop, i.e. sleeps and
//     then goes on to check something. A sleep as the last statement of a `for`
//     body is polling for a positive condition and stays allowed.
//
// Such an observation passes whenever the scheduler is late, so a broken join,
// callback order or launch passes on a slow machine. A long deadline (one
// second or more) is a watchdog for a hang and is allowed; what a test has to
// wait for is observed as an event (a channel the code closes, a hook it calls,
// a state the system reports, synctest.Wait), not as the end of an interval.
func quietWindows(raw []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", raw, 0)
	if err != nil {
		return nil, err
	}
	timeName := ""
	for _, spec := range file.Imports {
		if name, _ := strconv.Unquote(spec.Path.Value); name == "time" {
			timeName = "time"
			if spec.Name != nil {
				timeName = spec.Name.Name
			}
		}
	}
	if timeName == "" {
		return nil, nil
	}
	timeCall := func(n ast.Node, names ...string) (*ast.CallExpr, string, bool) {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return nil, "", false
		}
		fn, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return nil, "", false
		}
		if pkg, ok := fn.X.(*ast.Ident); !ok || pkg.Name != timeName {
			return nil, "", false
		}
		for _, name := range names {
			if fn.Sel.Name == name {
				return call, name, true
			}
		}
		return nil, "", false
	}
	var out []string
	flag := func(call *ast.CallExpr, name string) {
		if d, ok := shortDuration(call.Args[0], timeName); ok {
			out = append(out, fmt.Sprintf("%d: time.%s with a %s clock", fset.Position(call.Pos()).Line, name, d))
		}
	}
	// A sleep ending a loop body is polling; every other short sleep is a quiet interval.
	pollingSleeps := map[ast.Node]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch loop := n.(type) {
		case *ast.ForStmt:
			body = loop.Body
		case *ast.RangeStmt:
			body = loop.Body
		}
		if body != nil && len(body.List) > 0 {
			if stmt, ok := body.List[len(body.List)-1].(*ast.ExprStmt); ok {
				pollingSleeps[stmt.X] = true
			}
		}
		return true
	})
	ast.Inspect(file, func(n ast.Node) bool {
		if call, name, ok := timeCall(n, "After", "NewTimer"); ok {
			flag(call, name)
		}
		if call, name, ok := timeCall(n, "Sleep"); ok && !pollingSleeps[call] {
			flag(call, name)
		}
		return true
	})
	return out, nil
}

var units = map[string]int64{"Nanosecond": 1, "Microsecond": 1e3, "Millisecond": 1e6}

// shortDuration says whether e is a literal duration under one second, built
// from a number and a time unit (`20 * time.Millisecond`, `time.Millisecond`).
func shortDuration(e ast.Expr, timeName string) (string, bool) {
	unit := func(x ast.Expr) (int64, bool) {
		s, ok := x.(*ast.SelectorExpr)
		if !ok {
			return 0, false
		}
		if p, ok := s.X.(*ast.Ident); !ok || p.Name != timeName {
			return 0, false
		}
		v, ok := units[s.Sel.Name]
		return v, ok
	}
	if v, ok := unit(e); ok {
		return strconv.FormatInt(v, 10) + "ns", true
	}
	bin, ok := e.(*ast.BinaryExpr)
	if !ok || bin.Op != token.MUL {
		return "", false
	}
	for _, pair := range [][2]ast.Expr{{bin.X, bin.Y}, {bin.Y, bin.X}} {
		lit, ok := pair[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			continue
		}
		v, ok := unit(pair[1])
		if !ok {
			continue
		}
		n, ok := new(big.Int).SetString(lit.Value, 0)
		if !ok {
			continue
		}
		ns := new(big.Int).Mul(n, big.NewInt(v))
		if ns.Cmp(big.NewInt(1e9)) < 0 {
			return ns.String() + "ns", true
		}
	}
	return "", false
}

func quietIn(t *testing.T, body string) []string {
	t.Helper()
	got, err := quietWindows([]byte("package p\nimport \"time\"\nvar timeout = time.Second\nfunc f(done chan int, check func() bool) {\n" + body + "\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestQuietWindowGuardRecognizesShortClocks(t *testing.T) {
	for _, body := range []string{
		"select {\ncase <-done:\ncase <-time.After(20 * time.Millisecond):\n}",
		"select {\ncase <-done:\ncase <-time.After(time.Millisecond):\n}",
		"select {\ncase <-done:\ncase <-time.NewTimer(500 * time.Microsecond).C:\n}",
		"select {\ncase <-time.After(999 * time.Millisecond):\ncase <-done:\n}",
		"timer := time.NewTimer(20 * time.Millisecond)\nselect {\ncase <-done:\ncase <-timer.C:\n}",
		"time.Sleep(200 * time.Millisecond)\nif check() {\n}",
		"for i := 0; i < 3; i++ {\ntime.Sleep(10 * time.Millisecond)\n_ = check()\n}",
		"_ = time.After(20 * time.Millisecond)",
	} {
		if got := quietIn(t, body); len(got) != 1 {
			t.Errorf("%q: %v", body, got)
		}
	}
	aliased, _ := quietWindows([]byte("package p\nimport tm \"time\"\nfunc f(d chan int) {\nselect {\ncase <-d:\ncase <-tm.After(5 * tm.Millisecond):\n}\n}\n"))
	if len(aliased) != 1 {
		t.Errorf("an aliased time import was missed: %v", aliased)
	}
}

func TestQuietWindowGuardAllowsWatchdogsAndPositivePolling(t *testing.T) {
	for _, body := range []string{
		"select {\ncase <-done:\ncase <-time.After(5 * time.Second):\n}",
		"select {\ncase <-done:\ncase <-time.After(1000 * time.Millisecond):\n}",
		"select {\ncase <-done:\ncase <-time.After(timeout):\n}",
		"select {\ncase <-done:\ndefault:\n}",
		"for !check() {\ntime.Sleep(10 * time.Millisecond)\n}",
		"for {\nif check() {\nreturn\n}\ntime.Sleep(time.Millisecond)\n}",
		"for range 3 {\ntime.Sleep(time.Millisecond)\n}",
		"tick := time.NewTicker(10 * time.Millisecond)\n_ = tick",
		"time.Sleep(2 * time.Second)",
	} {
		if got := quietIn(t, body); len(got) != 0 {
			t.Errorf("%q: %v", body, got)
		}
	}
}

// Every Go test file of the repository: a negative observation by a short
// clock is not a test of a join, a callback order or a reap.
func TestRepositoryHasNoQuietWindows(t *testing.T) {
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
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		problems, err := quietWindows(raw)
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
	if checked < 150 {
		t.Fatalf("checked only %d test files", checked)
	}
}
