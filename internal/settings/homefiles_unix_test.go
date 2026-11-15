//go:build unix

package settings

import (
	"os"
	"syscall"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

var unixReplacements = []replacement{
	{
		name: "a named pipe",
		put: func(t *testing.T, path string, _ int64) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		message: func(l int64) string { return refusalOf(homefile.ErrNotRegular, l) },
	},
	{
		name: "a link to /dev/zero",
		put: func(t *testing.T, path string, _ int64) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/dev/zero", path); err != nil {
				t.Fatal(err)
			}
		},
		message: func(l int64) string { return refusalOf(homefile.ErrLink, l) },
	},
}

func TestSettingsFilesThatArePipesOrLinks(t *testing.T) {
	checkReplacements(t, unixReplacements)
}
