// Package web holds the browser app built by `make web`.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Files returns the built app and whether it holds an index.html.
func Files() (fs.FS, bool) {
	return filesFrom(dist)
}

func filesFrom(fsys fs.FS) (fs.FS, bool) {
	sub, err := fs.Sub(fsys, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
