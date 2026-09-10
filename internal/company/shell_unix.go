//go:build !windows

package company

func shellCommand() (string, []string) { return "sh", []string{"-i"} }
