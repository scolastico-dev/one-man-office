package plugins

import (
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

// CompanyExtension describes one browser entrypoint contributed by a
// plugin's company_load hook. Paths are relative to the plugin snapshot.
// Config is a defensive snapshot of the hook's resolved plugin configuration.
type CompanyExtension struct {
	Plugin     string
	Javascript string
	Files      []string
	Config     map[string]any
}

type companyExport struct {
	pattern   string
	directory bool
	glob      bool
}

func validateCompanyExports(root string, hook Hook) ([]companyExport, []string, string, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, nil, "", err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, nil, "", fmt.Errorf("plugin directory must be a real directory")
	}
	declarations := append(append([]string{}, hook.Files...), hook.Javascript)
	exports := make([]companyExport, 0, len(declarations))
	normalized := make([]string, 0, len(declarations))
	seen := make(map[string]bool, len(declarations))
	for _, declaration := range declarations {
		export, err := normalizeCompanyExport(declaration)
		if err != nil {
			return nil, nil, "", err
		}
		if seen[export.pattern] {
			continue
		}
		if !export.glob && !export.directory {
			if info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(export.pattern))); statErr == nil && info.IsDir() {
				export.directory = true
			}
		}
		seen[export.pattern] = true
		if err := validateCompanyExport(root, export); err != nil {
			return nil, nil, "", err
		}
		exports = append(exports, export)
		normalized = append(normalized, export.pattern)
	}
	javascript, err := normalizeCompanyExport(hook.Javascript)
	if err != nil {
		return nil, nil, "", err
	}
	return exports, normalized, javascript.pattern, nil
}

func normalizeCompanyExport(value string) (companyExport, error) {
	if value == "" || strings.ContainsRune(value, 0) {
		return companyExport{}, fmt.Errorf("web file path must not be empty or contain NUL")
	}
	value = strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(value, "/") || filepath.IsAbs(value) || filepath.VolumeName(value) != "" || (len(value) > 1 && value[1] == ':') {
		return companyExport{}, fmt.Errorf("web file path must stay inside the plugin")
	}
	trailingSlash := strings.HasSuffix(value, "/")
	parts := strings.Split(value, "/")
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			return companyExport{}, fmt.Errorf("web file path must stay inside the plugin")
		default:
			if _, err := pathpkg.Match(part, ""); err != nil {
				return companyExport{}, fmt.Errorf("invalid web file pattern %q: %w", value, err)
			}
			cleanParts = append(cleanParts, part)
		}
	}
	if len(cleanParts) == 0 {
		return companyExport{}, fmt.Errorf("web file path must not be empty")
	}
	pattern := strings.Join(cleanParts, "/")
	hasMeta := strings.ContainsAny(pattern, "*?[")
	return companyExport{pattern: pattern, directory: trailingSlash, glob: hasMeta}, nil
}

func validateCompanyJavascript(root, value string) (string, error) {
	export, err := normalizeCompanyExport(value)
	if err != nil {
		return "", err
	}
	if export.glob || export.directory {
		return "", fmt.Errorf("javascript must be an exact regular file")
	}
	info, err := companyPathInfo(root, export.pattern)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("javascript must be a regular file: %s", export.pattern)
	}
	return export.pattern, nil
}

func validateCompanyExport(root string, export companyExport) error {
	if export.glob {
		return validateCompanyGlob(root, export)
	}
	path := filepath.Join(root, filepath.FromSlash(export.pattern))
	info, err := companyPathInfo(root, export.pattern)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("web file must not be a symbolic link: %s", export.pattern)
	}
	if export.directory {
		if !info.IsDir() {
			return fmt.Errorf("web export must be a directory: %s", export.pattern)
		}
		return validateCompanyTree(path, export.pattern)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("web file must be a regular file: %s", export.pattern)
	}
	return nil
}

func companyPathInfo(root, relative string) (os.FileInfo, error) {
	current := root
	for _, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("web file must not be a symbolic link: %s", relative)
		}
	}
	return os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
}

func validateCompanyGlob(root string, export companyExport) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if info.Mode()&os.ModeSymlink != 0 && companyPatternCanMatchPrefix(export.pattern, rel) {
			return fmt.Errorf("web file must not be a symbolic link: %s", rel)
		}
		if !companyPatternMatch(export.pattern, rel) {
			return nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("web file must be a regular file: %s", rel)
		}
		return nil
	})
}

func companyPatternCanMatchPrefix(pattern, prefix string) bool {
	patterns, prefixes := strings.Split(pattern, "/"), strings.Split(filepath.ToSlash(prefix), "/")
	var match func(int, int) bool
	match = func(pi, ci int) bool {
		if ci == len(prefixes) {
			return true
		}
		if pi == len(patterns) {
			return false
		}
		if patterns[pi] == "**" {
			return match(pi+1, ci) || match(pi, ci+1)
		}
		if matched, err := pathpkg.Match(patterns[pi], prefixes[ci]); err == nil && matched {
			return match(pi+1, ci+1)
		}
		return false
	}
	return match(0, 0)
}

func validateCompanyTree(root, prefix string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return fmt.Errorf("web file must not be a symbolic link: %s", filepath.ToSlash(filepath.Join(prefix, rel)))
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("web file must be a regular file: %s", path)
		}
		return nil
	})
}

func companyPatternMatch(pattern, candidate string) bool {
	patterns, candidates := strings.Split(pattern, "/"), strings.Split(filepath.ToSlash(candidate), "/")
	var match func(int, int) bool
	match = func(pi, ci int) bool {
		if pi == len(patterns) {
			return ci == len(candidates)
		}
		if patterns[pi] == "**" {
			return match(pi+1, ci) || (ci < len(candidates) && match(pi, ci+1))
		}
		if ci >= len(candidates) {
			return false
		}
		matched, err := pathpkg.Match(patterns[pi], candidates[ci])
		return err == nil && matched && match(pi+1, ci+1)
	}
	return match(0, 0)
}

func companyRequestPath(value string) (string, bool) {
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" || strings.HasPrefix(value, "/") || filepath.VolumeName(value) != "" {
		return "", false
	}
	parts := strings.Split(value, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == ".." {
			return "", false
		}
		if part != "" && part != "." {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return "", false
	}
	return strings.Join(clean, "/"), true
}

func companyRegularPath(root, relative string) (string, bool) {
	rootInfo, err := os.Lstat(root)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return "", false
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	current := root
	for _, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", false
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

// CompanyExtensions returns browser hooks in the same stable order as the
// plugin runtime. The JavaScript entrypoint is always included in Files.
func (m *Manager) CompanyExtensions() []CompanyExtension {
	if m == nil {
		return nil
	}
	m.lifecycleMu.RLock()
	defer m.lifecycleMu.RUnlock()
	if m.closed {
		return nil
	}
	result := []CompanyExtension{}
	for _, loaded := range m.hooks {
		if loaded.hook.Event != EventCompanyLoad {
			continue
		}
		seen := map[string]bool{}
		files := make([]string, 0, len(loaded.hook.Files)+1)
		for _, export := range loaded.exports {
			path := export.pattern
			if !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
		}
		sort.Strings(files)
		result = append(result, CompanyExtension{
			Plugin:     loaded.plugin,
			Javascript: filepath.ToSlash(filepath.Clean(loaded.hook.Javascript)),
			Files:      files,
			Config:     copyJSONMap(loaded.config),
		})
	}
	return result
}

func copyJSONMap(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		copy[key] = copyJSONValue(value)
	}
	return copy
}

func copyJSONValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return copyJSONMap(value)
	case []any:
		copy := make([]any, len(value))
		for i, item := range value {
			copy[i] = copyJSONValue(item)
		}
		return copy
	default:
		return value
	}
}

// CompanyFile resolves a declared browser file without allowing callers to
// reach undeclared plugin content.
func (m *Manager) CompanyFile(plugin, path string) (string, bool) {
	if m == nil {
		return "", false
	}
	m.lifecycleMu.RLock()
	defer m.lifecycleMu.RUnlock()
	if m.closed {
		return "", false
	}
	path, ok := companyRequestPath(path)
	if !ok {
		return "", false
	}
	for _, loaded := range m.hooks {
		if loaded.plugin != plugin || loaded.hook.Event != EventCompanyLoad {
			continue
		}
		for _, export := range loaded.exports {
			matches := path == export.pattern
			if export.directory {
				matches = strings.HasPrefix(path, export.pattern+"/")
			} else if export.glob {
				matches = companyPatternMatch(export.pattern, path)
			}
			if matches {
				return companyRegularPath(loaded.dir, path)
			}
		}
	}
	return "", false
}
