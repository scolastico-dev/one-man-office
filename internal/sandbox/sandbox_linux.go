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
	_, _, err := platformConfig()
	return err
}

func platformConfig() (landlock.Config, int, error) {
	abi, err := llsys.LandlockGetABIVersion()
	if err != nil {
		return landlock.Config{}, 0, fmt.Errorf("%w: Landlock ABI query: %v", ErrUnsupported, err)
	}
	config, err := landlockConfigForABI(abi)
	return config, abi, err
}

func landlockConfigForABI(abi int) (landlock.Config, error) {
	// Device IOCTL control is the newest filesystem right this policy requires.
	// V8 adds thread synchronization, which go-landlock handles on older ABIs.
	switch {
	case abi >= 9:
		return landlock.V9, nil
	case abi == 8:
		return landlock.V8, nil
	case abi == 7:
		return landlock.V7, nil
	case abi == 6:
		return landlock.V6, nil
	case abi == 5:
		return landlock.V5, nil
	default:
		return landlock.Config{}, fmt.Errorf("%w: Landlock ABI %d is below required V5", ErrUnsupported, abi)
	}
}

func apply(p Policy) error {
	config, abi, err := platformConfig()
	if err != nil {
		return err
	}
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
