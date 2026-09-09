package plugins

import (
	"path/filepath"
	"sort"
)

// SupervisorExtension describes one browser entrypoint contributed by a
// plugin's on_supervisor_load hook. Paths are relative to the plugin snapshot.
type SupervisorExtension struct {
	Plugin     string
	Javascript string
	Files      []string
}

// SupervisorExtensions returns browser hooks in the same stable order as the
// plugin runtime. The JavaScript entrypoint is always included in Files.
func (m *Manager) SupervisorExtensions() []SupervisorExtension {
	if m == nil {
		return nil
	}
	m.lifecycleMu.RLock()
	defer m.lifecycleMu.RUnlock()
	if m.closed {
		return nil
	}
	result := []SupervisorExtension{}
	for _, loaded := range m.hooks {
		if loaded.hook.Event != EventSupervisorLoad {
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
		result = append(result, SupervisorExtension{Plugin: loaded.plugin, Javascript: filepath.ToSlash(filepath.Clean(loaded.hook.Javascript)), Files: files})
	}
	return result
}

// SupervisorFile resolves a declared browser file without allowing callers to
// reach undeclared plugin content.
func (m *Manager) SupervisorFile(plugin, path string) (string, bool) {
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
		if loaded.plugin != plugin || loaded.hook.Event != EventSupervisorLoad {
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
