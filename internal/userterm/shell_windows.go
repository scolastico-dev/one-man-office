//go:build windows

package userterm

import (
	"os"
	"os/exec"
)

// DefaultShell returns powershell.exe when it is on PATH, else %ComSpec%.
func DefaultShell() (string, []string) {
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return p, []string{"-NoLogo"}
	}
	if c := os.Getenv("ComSpec"); c != "" {
		return c, nil
	}
	return "cmd.exe", nil
}
