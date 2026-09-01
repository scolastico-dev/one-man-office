package pluginmanager

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"gopkg.in/yaml.v3"
)

func UpsertConfig(path, name string, plugin config.Plugin) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	doc, installed, err := loadInstalledMapping(path)
	if err != nil {
		return err
	}
	var value yaml.Node
	if err := value.Encode(plugin); err != nil {
		return err
	}
	setMappingValue(installed, name, &value)
	return writeConfigAtomic(path, doc)
}

func SetEnabled(path, name string, enabled bool) error {
	doc, installed, err := loadInstalledMapping(path)
	if err != nil {
		return err
	}
	entry := mappingNodeValue(installed, name)
	if entry == nil {
		return fmt.Errorf("plugin %q is not configured", name)
	}
	if entry.Kind != yaml.MappingNode {
		return fmt.Errorf("plugin %q configuration must be a mapping", name)
	}
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(enabled)}
	setMappingValue(entry, "enabled", value)
	return writeConfigAtomic(path, doc)
}

func loadInstalledMapping(path string) (*yaml.Node, *yaml.Node, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s: expected a YAML mapping", path)
	}
	root := doc.Content[0]
	plugins := mappingNodeValue(root, "plugins")
	if plugins == nil {
		plugins = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(root, "plugins", plugins)
	}
	if plugins.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s: plugins must be a mapping", path)
	}
	installed := mappingNodeValue(plugins, "installed")
	if installed == nil {
		installed = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(plugins, "installed", installed)
	}
	if installed.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s: plugins.installed must be a mapping", path)
	}
	return &doc, installed, nil
}

func mappingNodeValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func writeConfigAtomic(path string, doc *yaml.Node) error {
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".omo-plugins-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, &out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
