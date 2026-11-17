package mcpclients

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// fileConfig is what omo reads from a program's file: its user-level server
// entries and the project-level entries it keeps in the same file (Claude's
// local scope). Unknown keys are ignored everywhere.
type fileConfig struct {
	// servers are the user-level entries by name; a value that is not an
	// object is kept as it is and refused when mapped.
	servers map[string]any
	// projects are entries that belong to one project folder.
	projects []projectEntry
}

type projectEntry struct {
	folder, name string
}

// parseConfig reads data, the file of cli. The error says only what kind of
// file is broken: parser messages may quote the file's content.
func parseConfig(cli string, data []byte) (fileConfig, error) {
	var top map[string]any
	switch cli {
	case api.CLICodex:
		if err := toml.Unmarshal(data, &top); err != nil {
			return fileConfig{}, errors.New("the file is not valid TOML")
		}
	default:
		if len(strings.TrimSpace(string(data))) == 0 {
			return fileConfig{servers: map[string]any{}}, nil
		}
		if err := json.Unmarshal(data, &top); err != nil {
			return fileConfig{}, errors.New("the file is not valid JSON")
		}
	}
	key := "mcpServers"
	if cli == api.CLICodex {
		key = "mcp_servers"
	}
	fc := fileConfig{servers: map[string]any{}}
	if v, ok := top[key]; ok {
		m, ok := v.(map[string]any)
		if !ok {
			return fileConfig{}, fmt.Errorf("%s in the file is not a table of servers", key)
		}
		fc.servers = m
	}
	if cli == api.CLIClaude {
		if projects, ok := top["projects"].(map[string]any); ok {
			for _, folder := range sortedKeys(projects) {
				p, _ := projects[folder].(map[string]any)
				servers, _ := p["mcpServers"].(map[string]any)
				for _, name := range sortedKeys(servers) {
					fc.projects = append(fc.projects, projectEntry{folder: folder, name: name})
				}
			}
		}
	}
	return fc, nil
}

// str is v as a string, "" for anything else.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// strs is v as a list of strings; ok is false for anything but a list of
// strings (a missing value is an empty list).
func strs(v any) ([]string, bool) {
	if v == nil {
		return nil, true
	}
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// strMap is v as a map of strings; ok is false for anything else (a missing
// value is an empty map).
func strMap(v any) (map[string]string, bool) {
	if v == nil {
		return nil, true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(m))
	for k, x := range m {
		switch x := x.(type) {
		case string:
			out[k] = x
		case bool, float64, int64:
			out[k] = fmt.Sprint(x)
		default:
			return nil, false
		}
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, true
}

// seconds is a number of seconds as a duration; ok is false when v is not a
// positive number.
func seconds(v any) (api.Duration, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int64:
		f = float64(n)
	default:
		return 0, false
	}
	if f <= 0 || f > 365*24*3600 {
		return 0, false
	}
	return api.Duration(time.Duration(f * float64(time.Second))), true
}

// scopeList reads scopes given as a list or as one space-separated string.
func scopeList(v any) []string {
	if s, ok := v.(string); ok {
		return strings.Fields(s)
	}
	list, _ := strs(v)
	return slices.Clip(list)
}
