package pluginmanager

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// shippedDir is the repository's plugins folder, the tree the release
// workflow copies to the release branch.
const shippedDir = "../../plugins"

// shippedRepo is a local repository laid out as omo's release branch: the
// repository's plugins folder at plugins/. Plugins install from it as from
// the real release branch.
func shippedRepo(t *testing.T) *upstream {
	t.Helper()
	u := newUpstream(t)
	files := map[string]string{}
	err := filepath.WalkDir(shippedDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(shippedDir, p)
		if err != nil {
			return err
		}
		files["plugins/"+filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	u.publish(files)
	return u
}

// shipped is the settings of the shipped plugin folder as name, from repo.
func shipped(repo *upstream, name, folder string) api.PluginSettings {
	return api.PluginSettings{Name: name, Source: repo.bare, Subpath: "plugins/" + folder, Enabled: true}
}
