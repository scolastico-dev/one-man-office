//go:build !windows

package oldhome

import (
	"os"
	"path/filepath"
	"runtime"
)

// autostartPath is where the old omo wrote the entry: an XDG autostart
// file on Linux and the other Unix systems, a LaunchAgent on macOS.
func autostartPath(config string) string {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library", "LaunchAgents", entryName(config)+".plist")
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(root, "autostart", entryName(config)+".desktop")
}

// autostartEntry returns the entry's path when the file exists.
func autostartEntry(config string) string {
	path := autostartPath(config)
	if path == "" {
		return ""
	}
	if _, err := os.Lstat(path); err != nil {
		return ""
	}
	return path
}

func removeAutostart(config string) error {
	if err := os.Remove(autostartPath(config)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
