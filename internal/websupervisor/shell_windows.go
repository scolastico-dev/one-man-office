//go:build windows

package websupervisor

import "os"

func shellCommand() (string, []string) {
	command := os.Getenv("COMSPEC")
	if command == "" {
		command = "cmd.exe"
	}
	return command, []string{"/D"}
}
