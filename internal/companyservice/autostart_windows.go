package companyservice

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func installAutostart(executable, config string) (string, error) {
	command := strings.Join([]string{windows.EscapeArg(executable), "company", "autostart-run", windows.EscapeArg(config)}, " ")
	// Windows limits Run values to 260 characters. The settings stay in JSON,
	// keeping passwords and long company arguments out of the registry.
	if len(utf16.Encode([]rune(command))) > 260 {
		return "", fmt.Errorf("autostart command exceeds the Windows Run limit; use a shorter executable or OMO_HOME path")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	name := entryName(config)
	return `HKCU\` + runKey + `\` + name, key.SetStringValue(name, command)
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
	err = key.DeleteValue(entryName(config))
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}
