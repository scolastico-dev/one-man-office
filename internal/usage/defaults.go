package usage

import (
	"embed"
	"path"
	"strings"
)

//go:embed defaults/*.lua
var defaultScripts embed.FS

// Defaults returns the shipped usage scripts by name ("claude", "codex"),
// a fresh map on every call.
func Defaults() map[string]string {
	entries, err := defaultScripts.ReadDir("defaults")
	if err != nil {
		panic("usage: embedded default scripts: " + err.Error())
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		raw, err := defaultScripts.ReadFile(path.Join("defaults", e.Name()))
		if err != nil {
			panic("usage: embedded default scripts: " + err.Error())
		}
		out[strings.TrimSuffix(e.Name(), ".lua")] = string(raw)
	}
	return out
}
