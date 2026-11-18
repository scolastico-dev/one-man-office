// Command nodeshim is the browser tests' stand-in for the Node runtime that
// an editor artifact bundles: the plugin starts <artifact>/lib/node (with
// ".exe" on Windows) and the editor protocol fixture is a Node script. The
// shim is a native program of every platform, so no POSIX shell is involved.
// It runs the Node that started the tests, named by the file "node-target"
// next to the shim, with the arguments it got.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TargetFile is the file next to the shim that holds the absolute path of
// the Node to run.
const TargetFile = "node-target"

// resolve reads the Node path from the folder of the shim.
func resolve(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, TargetFile))
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(string(raw))
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("%s names %q, which is not an absolute path", TargetFile, target)
	}
	return target, nil
}

func main() {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nodeshim:", err)
		os.Exit(2)
	}
	target, err := resolve(filepath.Dir(self))
	if err != nil {
		fmt.Fprintln(os.Stderr, "nodeshim:", err)
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, "nodeshim:", run(target, os.Args[1:]))
	os.Exit(2)
}
