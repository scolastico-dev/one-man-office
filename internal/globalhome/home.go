// Package globalhome owns user-wide omo configuration and shared asset paths.
// Global configuration is independent of every office's .omo/omo.yaml.
package globalhome

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"gopkg.in/yaml.v3"
)

const defaults = "trusted_offices: []\ntemplate:\n  enabled: false\n  auto_sync: false\n  setup_never_ask: false\nplugins:\n  update_on_start: true\n  installed: {}\n"

type TemplateConfig struct {
	Enabled       bool `yaml:"enabled"`
	AutoSync      bool `yaml:"auto_sync"`
	SetupNeverAsk bool `yaml:"setup_never_ask"`
}

const knownPluginsDefaults = "[]\n"

const knownPluginsExample = `[
  {
    "name": "pushover",
    "description": "Send Pushover notifications for stable unread user mail and manual alerts",
    "official": true,
    "version": "1.0.0",
    "source": "https://github.com/scolastico-dev/one-man-office.git",
    "subpath": "plugins/pushover",
    "branch": "release"
  },
  {
    "name": "autoshutdown",
    "description": "Safely stop an office after a configurable idle period",
    "official": true,
    "version": "1.0.0",
    "source": "https://github.com/scolastico-dev/one-man-office.git",
    "subpath": "plugins/autoshutdown",
    "branch": "release"
  },
  {
    "name": "pullrequest",
    "description": "Create idempotent pull requests or merge requests for as-is jobs",
    "official": true,
    "version": "1.0.0",
    "source": "https://github.com/scolastico-dev/one-man-office.git",
    "subpath": "plugins/pullrequest",
    "branch": "release"
  }
]
`

type Config struct {
	TrustedOffices []string       `yaml:"trusted_offices"`
	Template       TemplateConfig `yaml:"template"`
	Plugins        config.Plugins `yaml:"plugins"`
}

type Home struct {
	Dir    string
	Config Config
}

// Dir resolves the user home without creating files. OMO_HOME must be absolute.
func Dir() (string, error) {
	user, err := os.UserHomeDir()
	if err != nil && os.Getenv("OMO_HOME") == "" && runtime.GOOS != "windows" {
		return "", err
	}
	return resolveDir(runtime.GOOS, user, os.Getenv("APPDATA"), os.Getenv("OMO_HOME"))
}

func resolveDir(platform, user, app, override string) (string, error) {
	if override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("OMO_HOME must be an absolute path")
		}
		return filepath.Clean(override), nil
	}
	if platform == "windows" {
		if app == "" || !filepath.IsAbs(app) {
			return "", fmt.Errorf("APPDATA must be an absolute path")
		}
		return filepath.Join(app, "omo"), nil
	}
	if user == "" || !filepath.IsAbs(user) {
		return "", fmt.Errorf("user home must be an absolute path")
	}
	return filepath.Join(user, ".local", "omo"), nil
}

// Open creates missing global scaffolding and strictly loads config.yaml.
func Open() (*Home, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"", "plugins", "extensions", "template"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			return nil, err
		}
	}
	h := &Home{Dir: dir}
	err = h.withLock(func() error {
		path := filepath.Join(dir, "config.yaml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := atomicWrite(path, []byte(defaults)); err != nil {
				return err
			}
		} else {
			if err != nil {
				return err
			}
		}
		known := filepath.Join(dir, "known_plugins.json")
		if _, err := os.Stat(known); os.IsNotExist(err) {
			if err := atomicWrite(known, []byte(knownPluginsDefaults)); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		example := filepath.Join(dir, "known_plugins.example.json")
		if _, err := os.Stat(example); os.IsNotExist(err) {
			if err := atomicWrite(example, []byte(knownPluginsExample)); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		return h.ensureBundledGlobalPlugin()
	})
	if err != nil {
		return nil, err
	}
	_, err = h.read()
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (h *Home) ensureBundledGlobalPlugin() error {
	if _, err := h.read(); err != nil {
		return err
	}
	target := filepath.Join(h.Dir, "plugins", "filebrowser")
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	entry, configured := h.Config.Plugins.Installed["filebrowser"]
	if configured && entry.Source != "builtin:filebrowser" {
		return nil
	}
	if !configured {
		entry = config.Plugin{Source: "builtin:filebrowser", Enabled: true}
	}
	_, err := pluginmanager.SyncAt(context.Background(), filepath.Join(h.Dir, "plugins"), filepath.Join(h.Dir, "config.yaml"), "filebrowser", entry)
	return err
}

func (h *Home) read() ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(h.Dir, "config.yaml"))
	if err != nil {
		return nil, err
	}
	cfg, err := decodeConfig(raw)
	if err != nil {
		return nil, err
	}
	h.Config = cfg
	return raw, nil
}

func decodeConfig(raw []byte) (Config, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Config{}, fmt.Errorf("global config: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return Config{}, fmt.Errorf("global config must be a mapping")
	}
	cfg := Config{TrustedOffices: []string{}, Template: TemplateConfig{}, Plugins: config.Plugins{UpdateOnStart: true, Installed: map[string]config.Plugin{}}}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("global config: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Config{}, fmt.Errorf("global config must contain exactly one YAML document")
	}
	for _, path := range cfg.TrustedOffices {
		if !filepath.IsAbs(path) {
			return Config{}, fmt.Errorf("trusted office must be an absolute path: %q", path)
		}
	}
	return cfg, nil
}

// CanonicalOffice identifies an existing office directory, resolving aliases.
func CanonicalOffice(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("office location is not a directory: %s", canonical)
	}
	return filepath.Clean(canonical), nil
}

// IsTrusted expects a canonical office path, as returned by CanonicalOffice.
func (h *Home) IsTrusted(canonical string) bool {
	for _, path := range h.Config.TrustedOffices {
		if sameTrustedOffice(path, canonical) {
			return true
		}
	}
	return false
}

func sameTrustedOffice(stored, candidate string) bool {
	return stored == candidate || (runtime.GOOS == "windows" && strings.EqualFold(stored, candidate))
}

// Trust serializes approvals across processes and atomically updates only the
// trust list, preserving plugin settings and comments in the YAML document.
func (h *Home) Trust(dir string) error {
	canonical, err := CanonicalOffice(dir)
	if err != nil {
		return err
	}
	return h.withLock(func() error {
		raw, err := h.read()
		if err != nil {
			return err
		}
		if h.IsTrusted(canonical) {
			return nil
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return err
		}
		if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
			return fmt.Errorf("global config must be a mapping")
		}
		mapping := doc.Content[0]
		var list *yaml.Node
		for i := 0; i < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == "trusted_offices" {
				list = mapping.Content[i+1]
				break
			}
		}
		if list == nil {
			list = &yaml.Node{}
			mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "trusted_offices"}, list)
		}
		values := append(h.Config.TrustedOffices, canonical)
		if err := list.Encode(values); err != nil {
			return err
		}
		data, err := yaml.Marshal(&doc)
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(h.Dir, "config.yaml"), data); err != nil {
			return err
		}
		h.Config.TrustedOffices = values
		return nil
	})
}

// Reorder updates the trusted office sequence after verifying that paths is an
// exact permutation of the currently stored strings.
func (h *Home) Reorder(paths []string) error {
	return h.withLock(func() error {
		raw, err := os.ReadFile(filepath.Join(h.Dir, "config.yaml"))
		if err != nil {
			return err
		}
		current, err := decodeConfig(raw)
		if err != nil {
			return err
		}
		if !samePermutation(current.TrustedOffices, paths) {
			return fmt.Errorf("paths must be an exact permutation of trusted offices")
		}

		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return err
		}
		if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
			return fmt.Errorf("global config must be a mapping")
		}
		mapping := doc.Content[0]
		var list *yaml.Node
		for i := 0; i < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == "trusted_offices" {
				list = mapping.Content[i+1]
				break
			}
		}
		if list == nil {
			list = &yaml.Node{}
			mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "trusted_offices"}, list)
		}
		if list.Kind != yaml.SequenceNode || len(list.Content) != len(paths) {
			if err := list.Encode(paths); err != nil {
				return err
			}
		} else {
			nodes := make(map[string]*yaml.Node, len(list.Content))
			for _, node := range list.Content {
				nodes[node.Value] = node
			}
			reordered := make([]*yaml.Node, 0, len(paths))
			for _, path := range paths {
				reordered = append(reordered, nodes[path])
			}
			list.Content = reordered
		}
		data, err := yaml.Marshal(&doc)
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(h.Dir, "config.yaml"), data); err != nil {
			return err
		}
		h.Config.TrustedOffices = append([]string(nil), paths...)
		return nil
	})
}

func samePermutation(current, requested []string) bool {
	if len(current) != len(requested) {
		return false
	}
	seen := make(map[string]struct{}, len(current))
	for _, path := range current {
		if _, ok := seen[path]; ok {
			return false
		}
		seen[path] = struct{}{}
	}
	for _, path := range requested {
		if _, ok := seen[path]; !ok {
			return false
		}
		delete(seen, path)
	}
	return len(seen) == 0
}

// Untrust removes an office approval without requiring the office to exist.
// It serializes the mutation and preserves all global configuration outside the
// trusted_offices YAML node.
func (h *Home) Untrust(dir string) error {
	candidate, err := CanonicalOffice(dir)
	if err != nil {
		candidate = dir
	}
	return h.withLock(func() error {
		raw, err := h.read()
		if err != nil {
			return err
		}
		filtered := make([]string, 0, len(h.Config.TrustedOffices))
		matched := false
		for _, stored := range h.Config.TrustedOffices {
			if sameTrustedOffice(stored, candidate) {
				matched = true
				continue
			}
			filtered = append(filtered, stored)
		}
		if !matched {
			return nil
		}

		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return err
		}
		if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
			return fmt.Errorf("global config must be a mapping")
		}
		mapping := doc.Content[0]
		var list *yaml.Node
		for i := 0; i < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == "trusted_offices" {
				list = mapping.Content[i+1]
				break
			}
		}
		if list == nil {
			return fmt.Errorf("global config is missing trusted_offices")
		}
		if err := list.Encode(filtered); err != nil {
			return err
		}
		data, err := yaml.Marshal(&doc)
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(h.Dir, "config.yaml"), data); err != nil {
			return err
		}
		h.Config.TrustedOffices = filtered
		return nil
	})
}

func (h *Home) withLock(fn func() error) error {
	f, err := os.OpenFile(filepath.Join(h.Dir, "config.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		return err
	}
	defer unlockFile(f)
	return fn()
}

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replaceFile(f.Name(), path)
}
