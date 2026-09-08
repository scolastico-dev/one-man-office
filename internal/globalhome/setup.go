package globalhome

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/yamlformat"
	"gopkg.in/yaml.v3"
)

// SaveSetupTemplate updates the reusable model/profile and role assignment
// choices without replacing other settings already present in the template.
func (h *Home) SaveSetupTemplate(models map[string]config.Profile, roles map[string]config.RoleModels) error {
	return h.withLock(func() error {
		dir := filepath.Join(h.Dir, "template", ".omo")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		path := filepath.Join(dir, "omo.yaml")
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			raw = []byte("{}\n")
		} else if err != nil {
			return err
		}
		var document yaml.Node
		if err := yaml.Unmarshal(raw, &document); err != nil {
			return fmt.Errorf("load setup template: %w", err)
		}
		if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
			return fmt.Errorf("setup template must be a mapping")
		}
		root := document.Content[0]
		// A missing template starts from `{}` above. Clear its flow style so the
		// generated file remains a normal, safely extensible block mapping.
		root.Style = 0
		modelsNode := &yaml.Node{}
		if err := modelsNode.Encode(models); err != nil {
			return err
		}
		rolesNode := &yaml.Node{}
		if err := rolesNode.Encode(roles); err != nil {
			return err
		}
		globalSetMappingValue(root, "models", modelsNode)
		globalSetMappingValue(root, "roles", rolesNode)
		out, err := yamlformat.EncodePreservingBlankLines(raw, root, 2)
		if err != nil {
			return err
		}
		return atomicWrite(path, out)
	})
}

// LoadSetupTemplate returns reusable setup choices when they have previously
// been saved. Other partial office-template keys are intentionally ignored.
func (h *Home) LoadSetupTemplate() (map[string]config.Profile, map[string]config.RoleModels, bool, error) {
	path := filepath.Join(h.Dir, "template", ".omo", "omo.yaml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	var document struct {
		Models map[string]config.Profile    `yaml:"models"`
		Roles  map[string]config.RoleModels `yaml:"roles"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, nil, false, fmt.Errorf("load setup template: %w", err)
	}
	hasChoices := len(document.Models) > 0 || len(document.Roles) > 0
	return document.Models, document.Roles, hasChoices, nil
}

// SetSetupNeverAsk records that future setup runs should skip the optional
// global-default questions while preserving unrelated global YAML content.
func (h *Home) SetSetupNeverAsk(never bool) error {
	return h.withLock(func() error {
		raw, err := h.read()
		if err != nil {
			return err
		}
		var document yaml.Node
		if err := yaml.Unmarshal(raw, &document); err != nil {
			return err
		}
		if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
			return fmt.Errorf("global config must be a mapping")
		}
		root := document.Content[0]
		template := globalMappingValue(root, "template")
		if template == nil {
			template = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			globalSetMappingValue(root, "template", template)
		}
		if template.Kind != yaml.MappingNode {
			return fmt.Errorf("global config template must be a mapping")
		}
		globalSetMappingValue(template, "setup_never_ask", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(never)})
		out, err := yamlformat.EncodePreservingBlankLines(raw, root, 2)
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(h.Dir, "config.yaml"), out); err != nil {
			return err
		}
		h.Config.Template.SetupNeverAsk = never
		return nil
	})
}

func globalMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func globalSetMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}
