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

// quietWindows finds a select that gives a short clock as one of its cases:
// `case <-time.After(20 * time.Millisecond):` (or a timer). Used to show that
// something has NOT happened, it passes whenever the scheduler is late, so a
// broken join or callback order passes on a slow machine. A long deadline
// (one second or more) is a watchdog for a hang and is allowed; what a test
// has to wait for is observed as an event (a channel the code closes, a hook
// it calls, synctest.Wait), not as the end of an interval.
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
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectStmt)
		if !ok {
			return true
		}
		for _, clause := range sel.Body.List {
			comm, ok := clause.(*ast.CommClause)
			if !ok || comm.Comm == nil {
				continue
			}
			ast.Inspect(comm.Comm, func(m ast.Node) bool {
				call, ok := m.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					return true
				}
				fn, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := fn.X.(*ast.Ident); !ok || pkg.Name != timeName || (fn.Sel.Name != "After" && fn.Sel.Name != "NewTimer") {
					return true
				}
				if d, ok := shortDuration(call.Args[0], timeName); ok {
					out = append(out, fmt.Sprintf("%d: a select with a %s clock (%s)", fset.Position(call.Pos()).Line, d, fn.Sel.Name))
				}
				return true
			})
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

func TestQuietWindowGuardRecognizesShortClocks(t *testing.T) {
	for _, body := range []string{
		"select {\ncase <-done:\ncase <-time.After(20 * time.Millisecond):\n}",
		"select {\ncase <-done:\ncase <-time.After(time.Millisecond):\n}",
		"select {\ncase <-done:\ncase <-time.NewTimer(500 * time.Microsecond).C:\n}",
		"select {\ncase <-time.After(999 * time.Millisecond):\ncase <-done:\n}",
	} {
		got, err := quietWindows([]byte("package p\nimport \"time\"\nfunc f(done chan int) {\n" + body + "\n}\n"))
		if err != nil || len(got) != 1 {
			t.Errorf("%q: %v %v", body, got, err)
		}
	}
	aliased, _ := quietWindows([]byte("package p\nimport tm \"time\"\nfunc f(d chan int) {\nselect {\ncase <-d:\ncase <-tm.After(5 * tm.Millisecond):\n}\n}\n"))
	if len(aliased) != 1 {
		t.Errorf("an aliased time import was missed: %v", aliased)
	}
}

func TestQuietWindowGuardAllowsWatchdogsAndOtherUses(t *testing.T) {
	for _, body := range []string{
		"select {\ncase <-done:\ncase <-time.After(5 * time.Second):\n}",
		"select {\ncase <-done:\ncase <-time.After(1000 * time.Millisecond):\n}",
		"select {\ncase <-done:\ncase <-time.After(timeout):\n}",
		"select {\ncase <-done:\ndefault:\n}",
		"time.Sleep(10 * time.Millisecond)",
		"_ = time.After(20 * time.Millisecond)",
	} {
		got, err := quietWindows([]byte("package p\nimport \"time\"\nvar timeout = time.Second\nfunc f(done chan int) {\n" + body + "\n}\n"))
		if err != nil || len(got) != 0 {
			t.Errorf("%q: %v %v", body, got, err)
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
