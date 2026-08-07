//go:build windows

package transport

import (
	"crypto/sha256"
	"fmt"
)

// Windows uses a named pipe; there is no filesystem socket or path limit.
func Endpoint(officeDir string) (actual, display string, cleanup func(), err error) {
	sum := sha256.Sum256([]byte(officeDir))
	actual = fmt.Sprintf(`\\.\pipe\omo-%x`, sum[:10])
	return actual, actual, func() {}, nil
}
