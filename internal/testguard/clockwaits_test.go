package testguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// clockWaits counts, by kind, the places where a test bounds a wait by a
// clock instead of waiting for the event: a race against time.After, a timer
// or ticker channel (time.NewTimer, time.NewTicker is a pacing tick and not
// counted, time.Tick), a context deadline (context.WithTimeout and
// WithDeadline), a connection deadline (Set*Deadline), and a polling loop that
// fails once time.Now() passed a deadline or time.Since grew past a limit. Under
// load a correct run fails by any of them, and a run that really hangs ends with
// the test binary's own time limit and a dump of every goroutine, which names the
// stuck wait better than a message does. Wait for the event itself, or list the
// file in retainedClockWaits with the reason.
func clockWaits(raw []byte) (map[string]int, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "", raw, 0)
	if err != nil {
		return nil, err
	}
	names := map[string]string{} // import name -> package path
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if path != "time" && path != "context" {
			continue
		}
		name := path
		if spec.Name != nil {
			name = spec.Name.Name
		}
		names[name] = path
	}
	found := map[string]int{}
	pkgCall := func(call *ast.CallExpr) (string, string) {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", ""
		}
		if id, ok := sel.X.(*ast.Ident); ok && names[id.Name] != "" {
			return names[id.Name], sel.Sel.Name
		}
		return "", sel.Sel.Name
	}
	clockCond := func(e ast.Expr) bool {
		hit := false
		ast.Inspect(e, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if pkg, name := pkgCall(call); pkg == "time" && name == "Since" {
				hit = true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "After" || sel.Sel.Name == "Before") {
				if inner, ok := sel.X.(*ast.CallExpr); ok {
					if pkg, name := pkgCall(inner); pkg == "time" && name == "Now" {
						hit = true
					}
				}
			}
			return true
		})
		return hit
	}
	failsOnly := func(b *ast.BlockStmt) bool {
		if len(b.List) == 0 {
			return false
		}
		for _, st := range b.List {
			es, ok := st.(*ast.ExprStmt)
			if !ok {
				return false
			}
			call, ok := es.X.(*ast.CallExpr)
			if !ok {
				return false
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			switch sel.Sel.Name {
			case "Fatal", "Fatalf", "Error", "Errorf", "FailNow":
			default:
				return false
			}
		}
		return true
	}
	var walk func(n ast.Node, inLoop bool)
	walk = func(n ast.Node, inLoop bool) {
		ast.Inspect(n, func(m ast.Node) bool {
			switch x := m.(type) {
			case *ast.ForStmt:
				if x != n {
					walk(x.Body, true)
					return false
				}
			case *ast.FuncLit:
				if x != n {
					walk(x.Body, false)
					return false
				}
			case *ast.IfStmt:
				if inLoop && x.Init == nil && x.Else == nil && clockCond(x.Cond) && failsOnly(x.Body) {
					found["poll deadline"]++
				}
			case *ast.CallExpr:
				pkg, name := pkgCall(x)
				switch {
				case pkg == "time" && (name == "After" || name == "NewTimer" || name == "Tick"):
					found["time."+name]++
				case pkg == "context" && (name == "WithTimeout" || name == "WithDeadline"):
					found["context."+name]++
				case pkg == "" && strings.HasPrefix(name, "Set") && strings.HasSuffix(name, "Deadline"):
					found[name]++
				}
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			walk(fn.Body, false)
		} else if gen, ok := decl.(*ast.GenDecl); ok {
			walk(gen, false)
		}
	}
	return found, nil
}

func TestClockWaitsAreFound(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want map[string]int
	}{
		"time.After aliased":    {`package p; import clk "time"; func f(c chan int) { select { case <-c: case <-clk.After(clk.Second): } }`, map[string]int{"time.After": 1}},
		"timer":                 {`package p; import "time"; func f() { _ = time.NewTimer(time.Second) }`, map[string]int{"time.NewTimer": 1}},
		"context deadline":      {`package p; import "context"; func f() { context.WithTimeout(context.Background(), 1); context.WithDeadline(context.Background(), x) }`, map[string]int{"context.WithTimeout": 1, "context.WithDeadline": 1}},
		"connection":            {`package p; func f(c conn) { c.SetReadDeadline(x) }`, map[string]int{"SetReadDeadline": 1}},
		"poll deadline":         {`package p; import "time"; func f(t T) { d := time.Now().Add(1); for !ok() { if time.Now().After(d) { t.Fatal("late") } } }`, map[string]int{"poll deadline": 1}},
		"poll since":            {`package p; import "time"; func f(t T) { s := time.Now(); for !ok() { if time.Since(s) > 1 { t.Fatalf("late") } } }`, map[string]int{"poll deadline": 1}},
		"deadline in a closure": {`package p; import "time"; func f(t T) { go func() { d := time.Now(); for { if time.Now().After(d) { t.Error("late") } } }() }`, map[string]int{"poll deadline": 1}},
		"allowed: ticker pace":  {`package p; import "time"; func f() { _ = time.NewTicker(time.Second); time.Sleep(1) }`, map[string]int{}},
		"allowed: lower bound":  {`package p; import "time"; func f(t T) { d := time.Now(); for time.Now().Before(d) { work() } }`, map[string]int{}},
		"allowed: no clock pkg": {`package p; func f(c chan int) { <-c; c.After(1); ctx.WithTimeout(1) }`, map[string]int{}},
	} {
		got, err := clockWaits([]byte(tc.src))
		if err != nil || len(got) != len(tc.want) {
			t.Errorf("%s: got %v, %v, want %v", name, got, err, tc.want)
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("%s: got %v, want %v", name, got, tc.want)
			}
		}
	}
}

// retainedClockWaits is the complete list of test files that still bound a
// wait by a clock, with how many of each kind and why none of them decides a
// hang of the code under test.
var retainedClockWaits = map[string]struct {
	kinds  map[string]int
	reason string
}{
	"internal/e2e/helpers_test.go":              {map[string]int{"time.After": 1, "context.WithTimeout": 1}, "cleanup after the test: an agent process that outlived its server is reported and killed so none is left behind, and the server's own shutdown deadline is an input of the code under test"},
	"internal/engine/process_test.go":           {map[string]int{"time.After": 1}, "cleanup after the test: an agent process that outlived the engine is reported and killed so none is left behind"},
	"internal/server/engine_process_test.go":    {map[string]int{"time.After": 1}, "cleanup after the test: an agent process that outlived the server is reported and killed so none is left behind"},
	"internal/engine/stop_process_test.go":      {map[string]int{"context.WithTimeout": 2}, "the deadline is the input of Shutdown under test (5 minutes: generous; 300 ms: Shutdown must end by its deadline), not a bound the test puts on a wait"},
	"internal/engine/stop_test.go":              {map[string]int{"context.WithTimeout": 3}, "an hour-long deadline given to Shutdown as its input; nothing waits for it"},
	"internal/filelock/lock_test.go":            {map[string]int{"context.WithTimeout": 1}, "the deadline is the input of Acquire under test: the lock must give up when it passes"},
	"internal/gitops/diff_timeout_unix_test.go": {map[string]int{"context.WithTimeout": 1}, "the deadline is the input of the diff under test: a diff must be cut off when it passes"},
	"internal/mcprelay/hostile_test.go":         {map[string]int{"context.WithTimeout": 1}, "the deadline is the input of the call under test: a hostile server must not hold a call past it"},
	"internal/pluginmanager/lists_test.go":      {map[string]int{"context.WithTimeout": 1}, "the deadline is the input of the list fetch under test; it runs in virtual time"},
	"internal/mcprelay/stdio_test.go":           {map[string]int{"context.WithTimeout": 1}, "the real time limit that expiry hands to every call but the one a test ends itself"},
	"internal/server/plugin_tasks_test.go":      {map[string]int{"context.WithTimeout": 1}, "a probe program started by the test has no parent that could dump its goroutines, so its own request is bounded to let it exit and report"},
	"internal/service/service_test.go":          {map[string]int{"context.WithTimeout": 1}, "the deadline is the input of Stop under test: how long it may wait for the server"},
	"cmd/omo/smoke_test.go":                     {map[string]int{"context.WithTimeout": 2}, "the smoke test drives a built omo and its child programs; the bound makes a stuck child end with its own output instead of the binary's time limit"},
	"internal/relaytransport/mux_test.go":       {map[string]int{"SetReadDeadline": 2, "SetWriteDeadline": 1}, "connection deadlines are the product inputs under test, entirely within synctest virtual time; assertions verify timeout and deadline clearing, never impose a wall-clock bound on completion"},
	"internal/usage/tracker_test.go":            {map[string]int{"poll deadline": 1}, "the loop runs inside a synctest bubble: its deadline is virtual time, which advances only when every goroutine of the bubble is blocked, so it is no wall clock and decides a livelock deterministically"},
	"internal/mcpbridge/bridge_test.go":         {map[string]int{"time.After": 3, "context.WithTimeout": 2}, "waits on a separate bridge process whose stderr belongs in the failure and which must be killed, and one loop that re-emits until the bridge's event stream is attached"},
}

func TestTestsWaitForEventsNotClocks(t *testing.T) {
	root := filepath.Join("..", "..")
	found := map[string]map[string]int{}
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
		kinds, err := clockWaits(raw)
		if err != nil {
			return err
		}
		if len(kinds) > 0 {
			rel, _ := filepath.Rel(root, path)
			found[filepath.ToSlash(rel)] = kinds
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for f := range found {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		keep, ok := retainedClockWaits[f]
		switch {
		case f == "internal/testguard/clockwaits_test.go":
		case !ok:
			t.Errorf("%s bounds a wait by a clock %v: wait for the event itself, or list the file in retainedClockWaits with the reason", f, found[f])
		case fmt.Sprint(keep.kinds) != fmt.Sprint(found[f]):
			t.Errorf("%s bounds waits by a clock %v, the list says %v", f, found[f], keep.kinds)
		}
	}
	for f := range retainedClockWaits {
		if found[f] == nil {
			t.Errorf("%s is listed in retainedClockWaits but no longer bounds a wait by a clock: remove it from the list", f)
		}
	}
}
