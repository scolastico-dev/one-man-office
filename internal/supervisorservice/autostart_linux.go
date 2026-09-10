package supervisorservice

import (
	"os"
	"path/filepath"
)

func autostartPath(config string) (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "autostart", entryName(config)+".desktop"), nil
}
func installAutostart(executable, config string) (string, error) {
	path, err := autostartPath(config)
	if err != nil {
		return "", err
	}
	data, err := desktopEntry(executable, config)
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
