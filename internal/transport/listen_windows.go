//go:build windows

package transport

import (
	"fmt"
	"net"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Listen listens on the named pipe at endpoint. Only the current user and
// SYSTEM may connect.
func Listen(endpoint string) (net.Listener, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read current user: %w", err)
	}
	sddl := fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;SY)", user.User.Sid.String())
	return winio.ListenPipe(endpoint, &winio.PipeConfig{SecurityDescriptor: sddl})
}
