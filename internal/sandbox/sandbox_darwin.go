//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const seatbeltExecutable = "/usr/bin/sandbox-exec"

func platformCheck() error {
	info, err := os.Stat(seatbeltExecutable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("%w: %s is unavailable", ErrUnsupported, seatbeltExecutable)
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
	if err := closeInheritedFDs(3); err != nil {
		return err
	}
	return syscall.Exec(seatbeltExecutable, append([]string{seatbeltExecutable, "-f", profilePath, command}, args...), Environment(env, p))
}
