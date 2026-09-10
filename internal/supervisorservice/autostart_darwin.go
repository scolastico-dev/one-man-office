package supervisorservice

import (
	"os"
	"path/filepath"
)

func autostartPath(config string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", entryName(config)+".plist"), nil
}
func installAutostart(executable, config string) (string, error) {
	path, err := autostartPath(config)
	if err != nil {
		return "", err
	}
	data, err := launchAgent(executable, config)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	return path, writePrivate(path, data)
}
func removeAutostart(config string) error {
	path, err := autostartPath(config)
	if err != nil {
		return err
	}
	return removeFile(path)
}
