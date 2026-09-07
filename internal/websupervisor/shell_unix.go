//go:build !windows

package websupervisor

func shellCommand() (string, []string) { return "sh", []string{"-i"} }
