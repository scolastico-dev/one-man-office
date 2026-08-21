package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ExtensionsDir contains optional role-preset additions. A role uses either
// <role>.md or the .md fragments in <role>/, never both.
const ExtensionsDir = ".omo/extensions"

func loadExtensions(officeDir, role string) (string, error) {
	root := filepath.Join(officeDir, ExtensionsDir)
	filePath := filepath.Join(root, role+".md")
	dirPath := filepath.Join(root, role)
	fileInfo, fileErr := os.Stat(filePath)
	dirInfo, dirErr := os.Stat(dirPath)
	fileExists := fileErr == nil
	dirExists := dirErr == nil
	if fileErr != nil && !os.IsNotExist(fileErr) {
		return "", fileErr
	}
	if dirErr != nil && !os.IsNotExist(dirErr) {
		return "", dirErr
	}
	if fileExists && dirExists {
		return "", fmt.Errorf("prompt extension preset %q has both %s and %s; use either the file or directory form", role, filePath, dirPath)
	}
	if fileExists {
		if !fileInfo.Mode().IsRegular() {
			return "", fmt.Errorf("prompt extension %s is not a regular file", filePath)
		}
		raw, err := os.ReadFile(filePath)
		return string(raw), err
	}
	if !dirExists {
		return "", nil
	}
	if !dirInfo.IsDir() {
		return "", fmt.Errorf("prompt extension preset %s is not a directory", dirPath)
	}
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var fragments []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dirPath, entry.Name()))
		if err != nil {
			return "", err
		}
		fragments = append(fragments, string(raw))
	}
	return strings.Join(fragments, "\n\n"), nil
}
