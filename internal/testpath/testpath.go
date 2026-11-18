// Package testpath keeps the real agent programs of the developer's machine
// away from a test that starts the built omo binary. omo looks for claude,
// codex, agy and gemini on PATH at a first start and runs their usage
// scripts, which spends the developer's quota, so such a test hands omo a
// PATH without any folder that holds one of them.
package testpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Programs are the agent programs omo knows.
var Programs = []string{"claude", "codex", "agy", "gemini"}

// Without returns path (PATH syntax) without every folder in which one of
// the agent programs, including a shim of it, is found.
func Without(path string) string {
	var keep []string
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || holdsProgram(dir) {
			continue
		}
		keep = append(keep, dir)
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

func holdsProgram(dir string) bool {
	for _, p := range Programs {
		if found, err := exec.LookPath(filepath.Join(dir, p)); err == nil && found != "" {
			return true
		}
	}
	return false
}

// Reaches names the agent programs that path still finds; a test that starts
// omo asserts it is empty.
func Reaches(path string) []string {
	var found []string
	for _, p := range Programs {
		for _, dir := range filepath.SplitList(path) {
			if f, err := exec.LookPath(filepath.Join(dir, p)); err == nil && f != "" {
				found = append(found, p)
				break
			}
		}
	}
	return found
}

// Abs turns a slash-separated absolute path of a test ("/srv/omo") into one
// that is absolute on the platform the test runs on. A path that starts
// with a slash has no drive on Windows and is not absolute there, so tests
// that need "some absolute path" name it through Abs.
func Abs(path string) string {
	if runtime.GOOS == "windows" && strings.HasPrefix(path, "/") {
		return `C:` + filepath.FromSlash(path)
	}
	return filepath.FromSlash(path)
}
