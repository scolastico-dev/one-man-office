package testguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shell fixture's environment boundary is a map of variables, so it is
// tested on every platform. A gate in these files is allowed only around a
// real consumer, and a consumer whose program is missing fails with the way to
// install it: a missing dependency is not a platform that lacks a capability.
var shellFixtureFiles = []string{
	"internal/childenv/childenvtest/shell_test.go",
	"internal/childenv/childenvtest/shell_unix_test.go",
	"internal/server/shell_env_test.go",
	"internal/server/shell_isolation_test.go",
}

// retainedSkips lists every skip these files may keep, with the one capability
// it names: Bash 3.2 has no BASHOPTS variable to consume.
var retainedSkips = map[string]string{
	"internal/childenv/childenvtest/shell_unix_test.go": "Bash lacks BASHOPTS",
}

func repoFile(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", filepath.FromSlash(name))
}

func TestShellFixtureGatesStayNarrow(t *testing.T) {
	for _, name := range shellFixtureFiles {
		raw, err := os.ReadFile(repoFile(t, name))
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, raw, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		skips := 0
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !strings.HasPrefix(sel.Sel.Name, "Skip") {
				return true
			}
			skips++
			if want := retainedSkips[name]; want == "" || !strings.Contains(string(raw[call.Pos()-1:call.End()-1]), want) {
				t.Errorf("%s: a skip that is not the retained %q capability gate", name, want)
			}
			return true
		})
		if want := retainedSkips[name]; want != "" && skips != 1 {
			t.Errorf("%s: want exactly one retained skip, found %d", name, skips)
		}
		// Only consumers that need a POSIX shell are Unix-only.
		constrained := strings.Contains(string(raw), "//go:build")
		unixOnly := strings.HasSuffix(name, "_unix_test.go") || strings.HasSuffix(name, "shell_isolation_test.go")
		if constrained != unixOnly {
			t.Errorf("%s: build constraint present = %v; only the real-shell consumer files may carry one", name, constrained)
		}
	}
}

// Every workflow job that runs the Go suite needs the shells the shell
// fixtures drive, after a package index update (a stale index makes the
// install itself nondeterministic).
func TestWorkflowsThatRunTheGoSuiteInstallShells(t *testing.T) {
	files, err := filepath.Glob(repoFile(t, ".github/workflows/*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		runsGo := strings.Contains(text, "make check") || strings.Contains(text, "go test ./...") || strings.Contains(text, "scripts/stress/ci.sh")
		if !runsGo {
			continue
		}
		update := strings.Index(text, "sudo apt-get update")
		install := strings.Index(text, "sudo apt-get install -y --no-install-recommends bash zsh")
		if update < 0 || install < update {
			t.Errorf("%s runs the Go suite without 'apt-get update' followed by installing bash and zsh", filepath.Base(file))
		}
	}
}
