//go:build !windows

package globalhome

import "os"

func replaceFile(from, to string) error { return os.Rename(from, to) }

// protectFile is a no-op on Unix: the file mode already restricts access.
func protectFile(string) error { return nil }
