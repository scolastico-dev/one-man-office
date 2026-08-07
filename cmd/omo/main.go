package main

import (
	"fmt"
	"os"

	"github.com/scolastico-dev/one-man-office/internal/cli"
)

// version is stamped by the build (see the Makefile's -ldflags).
var version = "dev"

func main() {
	if err := cli.Root(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "omo:", err)
		os.Exit(1)
	}
}
