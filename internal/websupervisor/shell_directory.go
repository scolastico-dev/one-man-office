package websupervisor

import (
	"fmt"
	"os"
	"path/filepath"
)

func shellDirectory(path string) (string, error) {
	if path == "" {
		var err error
		path, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("shell directory is not a directory: %s", canonical)
	}
	return filepath.Clean(canonical), nil
}
