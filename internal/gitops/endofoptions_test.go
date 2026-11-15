package gitops

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// namingCommands are the git subcommands whose positional arguments name
// refs, revisions, folders or addresses that may come from settings or
// agents. A value given to one of them must not be readable as an option.
var namingCommands = map[string]bool{
	"rev-parse": true, "worktree": true, "push": true, "fetch": true, "clone": true,
	"checkout": true, "symbolic-ref": true, "show-ref": true, "check-ref-format": true,
}

// valueFlags take the argument after them as their value.
var valueFlags = map[string]bool{"-b": true, "-B": true, "-m": true, "-c": true}

// TestGitValuesComeAfterEndOfOptions fails when a git argument list in this
// package passes a value (anything but a string literal, or an expression
// that starts with a literal such as "refs/heads/"+name) to one of
// namingCommands before "--end-of-options" or "--". Such a value starting
// with "-" would be read as an option.
func TestGitValuesComeAfterEndOfOptions(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			var list []ast.Expr
			switch n := n.(type) {
			case *ast.CallExpr:
				list = n.Args
			case *ast.CompositeLit:
				list = n.Elts
			default:
				return true
			}
			sub := -1
			for i, e := range list {
				if s, ok := literal(e); ok && namingCommands[s] {
					sub = i
					break
				}
			}
			if sub < 0 {
				return true
			}
			checked++
			for i := sub + 1; i < len(list); i++ {
				e := list[i]
				if s, ok := literal(e); ok {
					if s == "--end-of-options" || s == "--" {
						break
					}
					if valueFlags[s] {
						i++
					}
					continue
				}
				if leadingLiteral(e) {
					continue
				}
				if _, ok := e.(*ast.Ident); ok && i == len(list)-1 {
					if call, ok := n.(*ast.CallExpr); ok && call.Ellipsis.IsValid() {
						continue // args... of a wrapper
					}
				}
				t.Errorf("%s: a value is passed to git %s before --end-of-options", fset.Position(e.Pos()), mustLiteral(list[sub]))
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("found no git argument list: the scan is broken")
	}
}

func literal(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func mustLiteral(e ast.Expr) string { s, _ := literal(e); return s }

// leadingLiteral reports whether e is a concatenation whose first part is a
// string literal that does not start with "-".
func leadingLiteral(e ast.Expr) bool {
	for {
		b, ok := e.(*ast.BinaryExpr)
		if !ok || b.Op != token.ADD {
			break
		}
		e = b.X
	}
	s, ok := literal(e)
	return ok && s != "" && !strings.HasPrefix(s, "-")
}

func TestAddWorktreeTakesValuesLiterally(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	if err := g.AddWorktreeFromBase(repo, filepath.Join(t.TempDir(), "w"), "-x", ""); err == nil {
		t.Error("an option-like branch was accepted")
	}
	// A folder that starts with "-" is a folder, not an option (relative
	// folders are taken from the repository).
	if err := g.AddWorktreeFromBase(repo, "--detach", "omo/x", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "--detach", "f.txt")); err != nil {
		t.Errorf("the worktree is not in the folder --detach: %v", err)
	}
	// A base that starts with "-" is a revision git does not know.
	if err := g.AddWorktreeFromBase(repo, filepath.Join(t.TempDir(), "w2"), "omo/y", "--orphan"); err == nil {
		t.Error("the base --orphan was read as an option")
	}
	if v, err := g.refValue(repo, "--all"); v != "" || err != nil {
		t.Errorf("refValue(--all) = %q, %v; want no commit", v, err)
	}
}
