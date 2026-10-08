//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func platformCheck() error {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return fmt.Errorf("%w: sandbox-exec: %v", ErrUnsupported, err)
	}
	return nil
}

func execute(p Policy, command string, args []string, env []string) error {
	if err := platformCheck(); err != nil {
		return err
	}
	var profile strings.Builder
	profile.WriteString("(version 1)\n(deny default)\n(allow process-exec* process-fork sysctl-read network*)\n")
	profile.WriteString("(allow mach-lookup)\n")
	for _, path := range p.ReadPaths {
		profile.WriteString("(allow file-read* (subpath " + strconv.Quote(path) + "))\n")
	}
	for _, path := range p.WriteDirs {
		profile.WriteString("(allow file-read* file-write* (subpath " + strconv.Quote(path) + "))\n")
	}
	for _, path := range p.WriteFiles {
		profile.WriteString("(allow file-read* file-write* (literal " + strconv.Quote(path) + "))\n")
	}
	profile.WriteString("(allow file-read* file-write* (subpath \"/dev/tty\"))\n")
	profilePath := p.PrivateHome + "/seatbelt.sb"
	if err := os.WriteFile(profilePath, []byte(profile.String()), 0600); err != nil {
		return err
	}
	for fd := uintptr(3); fd < 1024; fd++ {
		syscall.CloseOnExec(int(fd))
	}
	sandboxExec, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return err
	}
	return syscall.Exec(sandboxExec, append([]string{sandboxExec, "-f", profilePath, command}, args...), Environment(env, p))
}
