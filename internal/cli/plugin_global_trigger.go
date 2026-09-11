package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

func triggerGlobalPlugin(ctx context.Context, name, action string, args []string) (string, error) {
	home, err := globalhome.Open()
	if err != nil {
		return "", err
	}
	database, err := db.Open(filepath.Join(home.Dir, "plugins.db"))
	if err != nil {
		return "", fmt.Errorf("open global plugin storage: %w", err)
	}
	defer database.Close()
	settings := make(map[string]plugins.Settings, len(home.Config.Plugins.Installed))
	for pluginName, configured := range home.Config.Plugins.Installed {
		settings[pluginName] = plugins.Settings{Enabled: configured.Enabled, Config: configured.Config}
	}
	manager, err := plugins.LoadSourcesContextWithOptions(ctx, home.Dir, database, plugins.Options{LogLines: plugins.DefaultLogLines}, plugins.Source{
		Root: filepath.Join(home.Dir, "plugins"), Configured: settings, Shared: true,
	})
	if err != nil {
		return "", fmt.Errorf("load global plugins: %w", err)
	}
	defer manager.Close()
	triggerResult, err := manager.TriggerManualContextWithRoleAndDataResult(ctx, name, action, "user", "user", args, nil)
	if err != nil {
		return "", fmt.Errorf("trigger global plugin: %w", err)
	}
	if triggerResult.Value == nil {
		return "", nil
	}
	result, ok := triggerResult.Value.(string)
	if !ok {
		return "", fmt.Errorf("global plugin result must be a string")
	}
	return result, nil
}
