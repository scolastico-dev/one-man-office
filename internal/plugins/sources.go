package plugins

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source is an installation directory and the settings owned by that scope.
// Later sources shadow earlier ones by directory/configuration key, including
// disabled or missing managed installations.
type Source struct {
	Root       string
	Configured map[string]Settings
	// Shared roots coordinate with managed updates and are snapshotted for
	// the runtime lifetime. Office-local sources retain their editable files.
	Shared bool
}

type pluginDirectory struct {
	name     string
	dir      string
	settings Settings
	managed  bool
	shared   bool
}

func selectDirectories(sources []Source) ([]pluginDirectory, error) {
	selected := map[string]pluginDirectory{}
	for _, source := range sources {
		if err := os.MkdirAll(source.Root, 0755); err != nil {
			return nil, err
		}
		for name, settings := range source.Configured {
			selected[name] = pluginDirectory{name: name, settings: settings, managed: true}
		}
		entries, err := os.ReadDir(source.Root)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			settings, managed := source.Configured[entry.Name()]
			selected[entry.Name()] = pluginDirectory{name: entry.Name(), dir: filepath.Join(source.Root, entry.Name()), settings: settings, managed: managed, shared: source.Shared}
		}
	}
	result := make([]pluginDirectory, 0, len(selected))
	for _, entry := range selected {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}
