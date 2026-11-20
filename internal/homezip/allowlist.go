// Package homezip exports omo's settings as a zip file and imports such a
// zip: the user's configuration files of the omo home (one explicit
// allowlist), a manifest, and on request a consistent snapshot of the
// database. An import is checked completely before anything changes, then
// written all at once, keeping copies of the replaced files in the home.
package homezip

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one kind of file a settings zip carries: a file of the home
// ("settings.yaml") or every file of a folder with one extension
// ("profiles/*.yaml"), named relative to the home with slashes.
type Entry struct {
	Pattern string
	// Server marks server.yaml: an import applies it only when asked.
	Server bool
}

// Exported is the allowlist: every file of the omo home a settings zip
// carries, and the only paths an import accepts besides manifest.json and
// omo.db. Everything at the top of the home that is not configuration is in
// NotExported; a test fails for a file or folder of a used home that is in
// neither list, and when the settings store knows a file this list lacks.
var Exported = []Entry{
	// General settings: CEO, limits, health, review and merge defaults,
	// the agents' environment, workspace folders, branch names, usage
	// thresholds, interface choices.
	{Pattern: "settings.yaml"},
	// Start options: listen host and port, sign-in, allowed hosts,
	// public_url. They belong to one machine, so an import writes them only
	// when the user asks for it; they apply at the next start.
	{Pattern: "server.yaml", Server: true},
	// One file per project: its folder, name, switch and merge modes.
	{Pattern: "projects/*.yaml"},
	// Agent profiles: program, arguments, account folder, environment,
	// models, usage script.
	{Pattern: "profiles/*.yaml"},
	// Which models each role uses, and how it picks among them.
	{Pattern: "roles/*.yaml"},
	// Prompt overrides and the user's instructions per role.
	{Pattern: "prompts/*.md"},
	// Usage scripts the user changed or added.
	{Pattern: "usage/*.lua"},
	// Skill sets: repository, branch or tag, updates, text per role.
	{Pattern: "skillsets/*.yaml"},
	// Plugin settings: source, folder, ref, switch, configuration. The
	// plugin's files are not in the zip; the Plugins page installs them
	// again from their source.
	{Pattern: "plugins/*.yaml"},
	// MCP servers omo relays: command, address, headers, environment and
	// OAuth client settings (values as stored, including tokens). omo's
	// sign-in at a server (its OAuth tokens) is in the database.
	{Pattern: "mcpservers/*.yaml"},
}

// NotExported names every other entry at the top of the omo home, and why a
// settings zip leaves it out.
var NotExported = map[string]string{
	"relay":         "private relay identity, admission secret, device trust and delivery state",
	"layout.json":   "the layout marker; every home writes its own",
	"token":         "this home's login token; another home keeps its own",
	"omo.db":        "the database: in the zip only on request, as a consistent snapshot",
	"omo.db-wal":    "the database's write-ahead log; the snapshot holds its content",
	"omo.db-shm":    "the database's shared-memory index",
	PendingDatabase: "an imported database that replaces omo.db at the next start",
	"run":           "the running server's socket, locks, state and staging files",
	"worktrees":     "the agents' git worktrees",
	"workdirs":      "working folders of tasks without a project",
	"ceo":           "the CEO's working folder",
	"logs":          "agent and session logs",
	"images":        "uploaded images; they belong to the tickets in the database",
	"skills":        "skill set checkouts, fetched again from their repositories",
	BackupFolder:    "copies of the files earlier imports replaced",
}

// NotExportedInFolders names what a folder of the allowlist may hold beside
// its settings files, and why it stays out of the zip.
var NotExportedInFolders = map[string]string{
	"plugins/<folder>": "installed plugin files, their fetch cache, staging and data folders (.cache, .data): installed again from the source",
	"<folder>/.<name>": "temporary files of an atomic write",
}

// PendingDatabase is the database an import placed next to omo.db; it
// replaces omo.db at the next start (SwapPendingDatabase).
const PendingDatabase = "omo.db.import"

// BackupFolder holds one folder per import with the files it replaced.
const BackupFolder = "backups"

// ManifestName and DatabaseName are the two entries of a zip outside the
// allowlist.
const (
	ManifestName = "manifest.json"
	DatabaseName = "omo.db"
)

// Allowed returns the allowlist entry rel (relative to the home, with
// slashes) belongs to.
func Allowed(rel string) (Entry, bool) {
	for _, e := range Exported {
		if matches(e.Pattern, rel) {
			return e, true
		}
	}
	return Entry{}, false
}

// matches reports whether rel is pattern, or a file directly in the
// pattern's folder with its extension and a name that is not hidden.
func matches(pattern, rel string) bool {
	dir, ext, folder := strings.Cut(pattern, "/*")
	if !folder {
		return rel == pattern
	}
	d, file := path.Split(rel)
	return d == dir+"/" && strings.HasSuffix(file, ext) && len(file) > len(ext) && !strings.HasPrefix(file, ".")
}

// folders are the folders of the allowlist.
func folders() map[string]bool {
	out := map[string]bool{}
	for _, e := range Exported {
		if dir, _, ok := strings.Cut(e.Pattern, "/*"); ok {
			out[dir] = true
		}
	}
	return out
}

// Classified returns an error unless name, an entry at the top of the home
// (dir for a folder), is exported or listed in NotExported.
func Classified(name string, dir bool) error {
	if _, ok := NotExported[name]; ok {
		return nil
	}
	if dir && folders()[name] {
		return nil
	}
	if e, ok := Allowed(name); ok && !dir && !strings.Contains(e.Pattern, "/") {
		return nil
	}
	return fmt.Errorf("%s is neither exported (homezip.Exported) nor listed in homezip.NotExported", name)
}

// Unclassified lists what the home at root holds that neither list
// classifies: entries at its top, and in the allowlist's folders anything
// but settings files, hidden temporary files and (in plugins) the plugin
// folders.
func Unclassified(root string) ([]string, error) {
	top, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range top {
		if err := Classified(e.Name(), e.IsDir()); err != nil {
			out = append(out, e.Name())
		}
	}
	for dir := range folders() {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			rel := dir + "/" + e.Name()
			switch {
			case strings.HasPrefix(e.Name(), "."):
			case e.IsDir() && dir == "plugins":
			case !e.IsDir() && e.Type().IsRegular():
				if _, ok := Allowed(rel); !ok {
					out = append(out, rel)
				}
			default:
				out = append(out, rel)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
