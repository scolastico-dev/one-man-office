//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"syscall"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsys "github.com/landlock-lsm/go-landlock/landlock/syscall"
	"golang.org/x/sys/unix"
)

func platformCheck() error {
	abi, err := llsys.LandlockGetABIVersion()
	if err != nil {
		return fmt.Errorf("%w: Landlock ABI query: %v", ErrUnsupported, err)
	}
	if abi < 8 {
		return fmt.Errorf("%w: Landlock ABI %d is below required V8", ErrUnsupported, abi)
	}
	return nil
}

func apply(p Policy) error {
	if err := platformCheck(); err != nil {
		return err
	}
	abi, _ := llsys.LandlockGetABIVersion()
	var rules []landlock.Rule
	for _, path := range p.ReadPaths {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			rules = append(rules, landlock.RODirs(path))
		} else {
			rules = append(rules, landlock.ROFiles(path))
		}
	}
	for _, path := range p.WriteDirs {
		rules = append(rules, landlock.RWDirs(path))
	}
	for _, path := range p.WriteFiles {
		rule := landlock.RWFiles(path)
		if path == p.Socket && abi >= 9 {
			rule = rule.WithResolveUnix()
		}
		if path == "/dev/null" || path == p.PTY {
			rule = rule.WithIoctlDev()
		}
		rules = append(rules, rule)
	}
	config := landlock.V8
	if abi >= 9 {
		config = landlock.V9
	}
	if err := config.RestrictPaths(rules...); err != nil {
		return fmt.Errorf("apply Landlock policy: %w", err)
	}
	return nil
}

func execute(p Policy, command string, args []string, env []string) error {
	if err := apply(p); err != nil {
		return err
	}
	if err := unix.CloseRange(3, ^uint(0), 0); err != nil {
		return fmt.Errorf("close inherited descriptors: %w", err)
	}
	return syscall.Exec(command, append([]string{command}, args...), Environment(env, p))
}
