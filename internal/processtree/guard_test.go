package processtree

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Each exception is a function, not a file-wide permission for new helpers.
var directLaunches = map[string]string{
	"internal/service/service.go:StartDetached":              "detached omo server outlives the invoking CLI",
	"internal/childenv/childenvtest/build.go:GoBuild":        "test setup runs the host Go compiler; processtree's own tests import childenvtest, so it cannot import processtree",
	"internal/fakeagent/ops.go:git":                          "scripted test agent",
	"internal/fakeagent/ops.go:opShBlock":                    "scripted test agent",
	"internal/usage/helpers.go:defaultKeychain":              "bounded macOS keychain lookup; no tree cleanup contract",
	"internal/usage/helpers.go:runAgyUsage":                  "bounded usage lookup; no tree cleanup contract",
	"internal/mcpclients/clients.go:run":                     "bounded configuration helper; no tree cleanup contract",
	"internal/agents/leftovers_unix.go:processStartTime":     "ps snapshot only",
	"internal/session/kill_other_unix.go:killSessionProcess": "ps snapshot only",
	"internal/webterm/process_unix.go:killTree":              "ps snapshot only",
	"internal/webterm/process_unix.go:startProcess":          "Unix PTY sets Setsid before exec",
	"internal/session/process_unix.go:startProcess":          "Unix PTY sets Setsid before exec",
}

type launchSource struct {
	path    string
	file    *ast.File
	aliases map[string]string
}
type launchAudit struct {
	sources   []launchSource
	factories map[string]map[int]bool
	fields    map[string]bool
}

func (a *launchAudit) add(set *token.FileSet, path string, src any) error {
	f, err := parser.ParseFile(set, path, src, 0)
	if err != nil {
		return err
	}
	aliases := map[string]string{}
	for _, imp := range f.Imports {
		name, _ := strconv.Unquote(imp.Path.Value)
		alias := filepath.Base(name)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		aliases[alias] = name
	}
	a.sources = append(a.sources, launchSource{path, f, aliases})
	return nil
}

func commandType(expr ast.Expr, aliases map[string]string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && aliases[pkg.Name] == "os/exec" && sel.Sel.Name == "Cmd"
}

// Index all platform files and command-returning factories before auditing
// execution. A factory consumer needs no os/exec import of its own.
func (a *launchAudit) index() {
	a.factories = map[string]map[int]bool{}
	a.fields = map[string]bool{}
	for _, s := range a.sources {
		dir := filepath.ToSlash(filepath.Dir(s.path))
		for _, decl := range s.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Type.Results == nil {
				continue
			}
			index := 0
			for _, result := range fn.Type.Results.List {
				count := max(1, len(result.Names))
				if commandType(result.Type, s.aliases) {
					key := dir + ":" + fn.Name.Name
					if a.factories[key] == nil {
						a.factories[key] = map[int]bool{}
					}
					for i := 0; i < count; i++ {
						a.factories[key][index+i] = true
					}
				}
				index += count
			}
		}
		ast.Inspect(s.file, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range st.Fields.List {
				if commandType(field.Type, s.aliases) {
					for _, name := range field.Names {
						a.fields[dir+":"+name.Name] = true
					}
				}
			}
			return true
		})
	}
}

func (a *launchAudit) returnsCommand(expr ast.Expr, s launchSource, index int) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	dir := filepath.ToSlash(filepath.Dir(s.path))
	name := ""
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		name = fn.Sel.Name
		if pkg, ok := fn.X.(*ast.Ident); ok {
			path := s.aliases[pkg.Name]
			if path == "os/exec" {
				return index == 0 && (name == "Command" || name == "CommandContext")
			}
			if strings.HasPrefix(path, "github.com/scolastico-dev/one-man-office/") {
				dir = strings.TrimPrefix(path, "github.com/scolastico-dev/one-man-office/")
			}
		}
	}
	return a.factories[dir+":"+name][index]
}

func (a *launchAudit) command(expr ast.Expr, s launchSource, depth int) bool {
	if depth > 20 {
		return false
	}
	switch e := expr.(type) {
	case *ast.CallExpr:
		return a.returnsCommand(e, s, 0)
	case *ast.SelectorExpr:
		return a.fields[filepath.ToSlash(filepath.Dir(s.path))+":"+e.Sel.Name]
	case *ast.Ident:
		if e.Obj == nil {
			return false
		}
		switch decl := e.Obj.Decl.(type) {
		case *ast.Field:
			return commandType(decl.Type, s.aliases)
		case *ast.ValueSpec:
			if commandType(decl.Type, s.aliases) {
				return true
			}
			for i, name := range decl.Names {
				if name.Obj == e.Obj && i < len(decl.Values) {
					return a.command(decl.Values[i], s, depth+1)
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range decl.Lhs {
				name, ok := lhs.(*ast.Ident)
				if !ok || name.Obj != e.Obj {
					continue
				}
				if len(decl.Rhs) == 1 {
					if a.returnsCommand(decl.Rhs[0], s, i) {
						return true
					}
					if i == 0 {
						return a.command(decl.Rhs[0], s, depth+1)
					}
				}
				if i < len(decl.Rhs) {
					return a.command(decl.Rhs[i], s, depth+1)
				}
			}
		}
	}
	return false
}

func (a *launchAudit) violations(set *token.FileSet) []string {
	a.index()
	var found []string
	for _, s := range a.sources {
		if strings.HasPrefix(s.path, "internal/processtree/") {
			continue
		}
		for _, decl := range s.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if directLaunches[s.path+":"+fn.Name.Name] != "" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg := ""
				if recv, ok := sel.X.(*ast.Ident); ok {
					pkg = s.aliases[recv.Name]
				}
				forbidden := false
				switch pkg {
				case "golang.org/x/sys/windows", "syscall", "os":
					switch sel.Sel.Name {
					case "CreateProcess", "CreateProcessAsUser", "StartProcess", "ForkExec", "AssignProcessToJobObject", "CreateJobObject":
						forbidden = true
					}
				case "github.com/UserExistsError/conpty", "github.com/creack/pty":
					forbidden = strings.HasPrefix(sel.Sel.Name, "Start")
				}
				switch sel.Sel.Name {
				case "Start", "Run", "Output", "CombinedOutput":
					forbidden = forbidden || a.command(sel.X, s, 0)
				}
				if forbidden {
					found = append(found, set.Position(call.Pos()).String()+": direct "+sel.Sel.Name+" launch; use internal/processtree")
				}
				return true
			})
		}
	}
	return found
}

func TestContainedLaunchesUseSharedPath(t *testing.T) {
	root := filepath.Join("..", "..")
	set := token.NewFileSet()
	a := &launchAudit{}
	for _, top := range []string{"internal", "cmd", "plugins"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return a.add(set, filepath.ToSlash(rel), src)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(a.sources) < 50 {
		t.Fatalf("only %d files scanned", len(a.sources))
	}
	for _, violation := range a.violations(set) {
		t.Error(violation)
	}
}

func TestGuardCatchesFactoriesAliasesAndFields(t *testing.T) {
	cases := []struct{ name, source string }{
		{"factory consumer", `func run(){ cmd,_:=factory(); cmd.Start() }`},
		{"misleading group name", `func run(){ group,_:=factory(); group.Run() }`},
		{"misleading constructor", `func newProcessGroup() *exec.Cmd { return exec.Command("x") }; func run(){ group:=newProcessGroup(); group.Start() }`},
		{"alias", `func run(){ cmd,_:=factory(); other:=cmd; other.Output() }`},
		{"field", `type holder struct{ cmd *exec.Cmd }; func run(h holder){ h.cmd.CombinedOutput() }`},
		{"parameter", `func run(cmd *exec.Cmd){ cmd.Start() }`},
		{"inline", `func run(){ exec.Command("x").Start() }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := token.NewFileSet()
			a := &launchAudit{}
			if err := a.add(set, "internal/example/factory.go", `package example; import "os/exec"; func factory() (*exec.Cmd,error){ return exec.Command("x"),nil }`); err != nil {
				t.Fatal(err)
			}
			// Factory consumer intentionally has no import; other fixtures need exec
			// only to declare their command types/constructors.
			imports := ""
			if strings.Contains(tc.source, "exec.") {
				imports = `import "os/exec";`
			}
			if err := a.add(set, "internal/example/consumer.go", "package example;"+imports+tc.source); err != nil {
				t.Fatal(err)
			}
			if got := a.violations(set); len(got) != 1 {
				t.Fatalf("got %v, want one rejected direct launch", got)
			}
		})
	}
}
