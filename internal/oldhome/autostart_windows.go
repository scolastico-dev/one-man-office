package oldhome

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartEntry returns the old entry (a value of the current user's Run
// key) when it exists.
func autostartEntry(config string) string {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	if _, _, err := key.GetStringValue(entryName(config)); err != nil {
		return ""
	}
	return `HKCU\` + runKey + `\` + entryName(config)
}

func removeAutostart(config string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.DeleteValue(entryName(config)); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
