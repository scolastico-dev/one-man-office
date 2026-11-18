package testguard

import (
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

// clockWaits counts the calls of time.After in one Go source. A test that waits
// for an event by racing it against time.After decides a hang by a clock: under
// load a correct run fails, and a run that really hangs ends with the test
// binary's own time limit and a dump of every goroutine, which names the
// stuck wait better than a message does. Wait for the event itself.
func clockWaits(raw []byte) (int, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "", raw, 0)
	if err != nil {
		return 0, err
	}
	name := ""
	for _, spec := range file.Imports {
		if path, _ := strconv.Unquote(spec.Path.Value); path == "time" {
			name = "time"
			if spec.Name != nil {
				name = spec.Name.Name
			}
		}
	}
	n := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || name == "" {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "After" {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == name {
				n++
			}
		}
		return true
	})
	return n, nil
}

func TestClockWaitsAreFound(t *testing.T) {
	n, err := clockWaits([]byte(`package p; import clk "time"; func f(c chan int) { select { case <-c: case <-clk.After(clk.Second): } ; _ = clk.After(1) }`))
	if err != nil || n != 2 {
		t.Fatalf("got %d, %v", n, err)
	}
	if n, _ := clockWaits([]byte(`package p; func f(c chan int) { <-c; c.After(1) }`)); n != 0 {
		t.Fatalf("a call without the time package counted: %d", n)
	}
}

// retainedClockWaits is the complete list of test files that still call
// time.After, with how many calls and why each is not a verdict on a hang.
var retainedClockWaits = map[string]struct {
	calls  int
	reason string
}{
	"internal/e2e/helpers_test.go":           {1, "cleanup after the test: an agent process that outlived its server is reported and killed so none is left behind"},
	"internal/engine/process_test.go":        {1, "cleanup after the test: an agent process that outlived the engine is reported and killed so none is left behind"},
	"internal/server/engine_process_test.go": {1, "cleanup after the test: an agent process that outlived the server is reported and killed so none is left behind"},
	"internal/gitops/export_test.go":         {1, "one-sided window: a second fetch must not enter while the first holds the repository; a short window can only miss a violation, never fail a correct run, and the boundary has no observable point without a seam in the product"},
	"internal/plugins/tasks_test.go":         {1, "one-sided window: a concurrent Close must not return before the callback ends; same reason as the fetch window"},
	"internal/plugins/forwards_test.go":      {1, "one-sided window: cleanup must wait for an in-flight forward creation; same reason as the fetch window"},
	"internal/mcpbridge/bridge_test.go":      {3, "waits on a separate bridge process whose stderr belongs in the failure and which must be killed, and one loop that re-emits until the bridge's event stream is attached"},
}

func TestTestsWaitForEventsNotClocks(t *testing.T) {
	root := filepath.Join("..", "..")
	found := map[string]int{}
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
		n, err := clockWaits(raw)
		if err != nil {
			return err
		}
		if n > 0 {
			rel, _ := filepath.Rel(root, path)
			found[filepath.ToSlash(rel)] = n
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
		if !ok {
			t.Errorf("%s calls time.After %d times: wait for the event itself, or list the file in retainedClockWaits with the reason", f, found[f])
		} else if keep.calls != found[f] {
			t.Errorf("%s calls time.After %d times, the list says %d", f, found[f], keep.calls)
		}
	}
	for f := range retainedClockWaits {
		if found[f] == 0 {
			t.Errorf("%s is listed in retainedClockWaits but no longer calls time.After: remove it from the list", f)
		}
	}
}
