package supervisorservice

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/filelock"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
)

// Registration preserves the argument vector and launch context without shell
// interpolation. Authentication flags belong only in this private file.
type Registration struct {
	Args      []string `json:"args"`
	Directory string   `json:"directory"`
	Home      string   `json:"omo_home"`
	Path      string   `json:"path"`
}

func RegisterAutostart(ctx context.Context, executable string, args []string) (string, error) {
	dir, err := prepareDirectory()
	if err != nil {
		return "", err
	}
	lock, err := filelock.Acquire(ctx, filepath.Join(dir, "autostart.lock"))
	if err != nil {
		return "", err
	}
	defer lock.Close()
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	home, err := globalhome.Dir()
	if err != nil {
		return "", err
	}
	config := filepath.Join(dir, "autostart.json")
	registration := Registration{Args: args, Directory: cwd, Home: home, Path: os.Getenv("PATH")}
	data, err := json.MarshalIndent(registration, "", "  ")
	if err != nil {
		return "", err
	}
	previous, readErr := os.ReadFile(config)
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", readErr
	}
	if err := writePrivate(config, data); err != nil {
		return "", err
	}
	location, err := installAutostart(executable, config)
	if err != nil {
		var rollback error
		if readErr == nil {
			rollback = writePrivate(config, previous)
		} else {
			rollback = os.Remove(config)
		}
		if rollback != nil {
			return "", fmt.Errorf("register autostart: %w (restore settings: %v)", err, rollback)
		}
		return "", err
	}
	return location, nil
}

func UnregisterAutostart(ctx context.Context) error {
	dir, err := directory()
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	lock, err := filelock.Acquire(ctx, filepath.Join(dir, "autostart.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	config := filepath.Join(dir, "autostart.json")
	if err := removeAutostart(config); err != nil {
		return err
	}
	if err := os.Remove(config); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func LoadAutostart(path string) (Registration, error) {
	var registration Registration
	data, err := os.ReadFile(path)
	if err != nil {
		return registration, err
	}
	if err = json.Unmarshal(data, &registration); err != nil {
		return registration, err
	}
	if !filepath.IsAbs(registration.Home) || !filepath.IsAbs(registration.Directory) || len(registration.Args) == 0 || registration.Args[0] != "supervisor" {
		return registration, fmt.Errorf("invalid supervisor autostart settings")
	}
	return registration, nil
}

func entryName(config string) string {
	sum := sha256.Sum256([]byte(config))
	return fmt.Sprintf("dev.scolastico.omo-supervisor-%x", sum[:8])
}

// desktopArg applies Exec quoting, then Desktop Entry string escaping.
// See https://specifications.freedesktop.org/desktop-entry/latest/exec-variables.html.
func desktopArg(value string) string {
	value = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "`", "\\`", "$", "\\$").Replace(value)
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return "\"" + strings.ReplaceAll(value, "%", "%%") + "\""
}

func desktopEntry(executable, config string) ([]byte, error) {
	// GIO checks the executable before expanding %% field escapes. Such a
	// filename would silently produce an entry the desktop cannot launch.
	if strings.Contains(executable, "%") {
		return nil, fmt.Errorf("Linux autostart requires an executable path without percent signs")
	}
	if strings.ContainsAny(executable+config, "\r\n\x00") {
		return nil, fmt.Errorf("autostart paths cannot contain line breaks or NUL")
	}
	return []byte("[Desktop Entry]\nType=Application\nName=omo supervisor\nComment=Local office and terminal dashboard\nTerminal=false\nExec=" + desktopArg(executable) + " supervisor autostart-run " + desktopArg(config) + "\n"), nil
}

func launchAgent(executable, config string) ([]byte, error) {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n<key>Label</key><string>")
	if err := xml.EscapeText(&b, []byte(entryName(config))); err != nil {
		return nil, err
	}
	b.WriteString("</string>\n<key>ProgramArguments</key><array>")
	for _, arg := range []string{executable, "supervisor", "autostart-run", config} {
		b.WriteString("<string>")
		if err := xml.EscapeText(&b, []byte(arg)); err != nil {
			return nil, err
		}
		b.WriteString("</string>")
	}
	b.WriteString("</array>\n<key>RunAtLoad</key><true/>\n</dict></plist>\n")
	return []byte(b.String()), nil
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
