package plugins

import (
	"path/filepath"
	"sort"
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
		for _, path := range append(append([]string{}, loaded.hook.Files...), loaded.hook.Javascript) {
			path = filepath.ToSlash(filepath.Clean(path))
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
	path = filepath.ToSlash(filepath.Clean(path))
	for _, loaded := range m.hooks {
		if loaded.plugin != plugin || loaded.hook.Event != EventCompanyLoad {
			continue
		}
		for _, declared := range append(append([]string{}, loaded.hook.Files...), loaded.hook.Javascript) {
			if filepath.ToSlash(filepath.Clean(declared)) == path {
				return filepath.Join(loaded.dir, filepath.FromSlash(path)), true
			}
		}
	}
	return "", false
}
