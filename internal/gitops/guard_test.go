package gitops

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

// gitStartsElsewhere are the places outside this package that start git
// themselves, each reviewed: none runs in a folder an agent controls.
var gitStartsElsewhere = map[string]string{
	// Plugin sources are cloned into and checked out in omo's own plugin
	// cache.
	"internal/pluginmanager/manager.go": "plugin cache",
	// check-ref-format needs no repository.
	"internal/plugins/plugins.go": "check-ref-format",
	// The fake agent is an agent: it runs git in its own worktree.
	"internal/fakeagent/ops.go": "test agent",
}

// TestGitStartsOnlyThroughCommand fails when code starts a git process
// without going through (*Git).command, the one place that sets a resolved
// worktree's repository explicitly and refuses to let git look for a
// repository in the agents' folder. Inside this package every process start
// must be in command (or the Windows tree kill); outside it only the
// reviewed places of gitStartsElsewhere may start git.
func TestGitStartsOnlyThroughCommand(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			inGitops := strings.HasPrefix(rel, "internal/gitops/")
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || (sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext") {
						return true
					}
					if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "exec" {
						return true
					}
					name := commandName(call)
					pos := fset.Position(call.Pos())
					switch {
					case inGitops:
						seen++
						if fn.Name.Name != "command" && !(rel == "internal/gitops/procgroup_windows.go" && name == "taskkill") {
							t.Errorf("%s:%d: %s starts a process outside (*Git).command", rel, pos.Line, fn.Name.Name)
						}
					case name == "git" && gitStartsElsewhere[rel] == "":
						t.Errorf("%s:%d: starts git itself; run it through internal/gitops", rel, pos.Line)
					case name == "" && (strings.HasPrefix(rel, "internal/engine/") || strings.HasPrefix(rel, "internal/server/")):
						t.Errorf("%s:%d: starts a process whose name is not a literal; if it may be git, run it through internal/gitops", rel, pos.Line)
					}
					return true
				})
			}
			return nil
		})
		if err != nil && !strings.Contains(err.Error(), "no such file") {
			t.Fatal(err)
		}
	}
	if seen == 0 {
		t.Fatal("found no process start in internal/gitops: the scan is broken")
	}
}

// commandName is the literal program name of an exec.Command or
// exec.CommandContext call, or "".
func commandName(call *ast.CallExpr) string {
	i := 0
	if sel := call.Fun.(*ast.SelectorExpr); sel.Sel.Name == "CommandContext" {
		i = 1
	}
	if len(call.Args) <= i {
		return ""
	}
	lit, ok := call.Args[i].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}
