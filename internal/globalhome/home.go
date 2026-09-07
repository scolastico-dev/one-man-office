// Package globalhome owns user-wide omo configuration and shared asset paths.
// Global configuration is independent of every office's .omo/omo.yaml.
package globalhome

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"gopkg.in/yaml.v3"
)

const defaults = "trusted_offices: []\nplugins:\n  update_on_start: true\n  installed: {}\n"

type Config struct {
	TrustedOffices []string       `yaml:"trusted_offices"`
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
			return atomicWrite(path, []byte(defaults))
		} else {
			return err
		}
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

func (h *Home) read() ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(h.Dir, "config.yaml"))
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("global config: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("global config must be a mapping")
	}
	cfg := Config{TrustedOffices: []string{}, Plugins: config.Plugins{UpdateOnStart: true, Installed: map[string]config.Plugin{}}}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("global config: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("global config must contain exactly one YAML document")
	}
	for _, path := range cfg.TrustedOffices {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("trusted office must be an absolute path: %q", path)
		}
	}
	h.Config = cfg
	return raw, nil
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
		if path == canonical || (runtime.GOOS == "windows" && strings.EqualFold(path, canonical)) {
			return true
		}
	}
	return false
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
