package office

import (
	"fmt"
	"sort"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/yamlformat"
	"gopkg.in/yaml.v3"
)

// SetupOptions contains the choices collected by interactive setup. Empty
// maps retain the historical provider-specific generated configuration.
type SetupOptions struct {
	Provider agentcli.Provider
	Models   map[string]config.Profile
	Roles    map[string]config.RoleModels
	Plugins  map[string]config.Plugin
}

// SetupCatalog is the set of detected profiles offered by interactive setup
// and the current role defaults after unavailable providers are filtered out.
type SetupCatalog struct {
	Models map[string]config.Profile
	Roles  map[string]config.RoleModels
}

type setupProfileDocument struct {
	Models map[string]config.Profile
	Roles  map[string]config.RoleModels
}

// SetupCatalogFor combines the built-in profile examples for installed CLIs.
// The selected provider supplies the preselected role assignments.
func SetupCatalogFor(provider agentcli.Provider, installed []agentcli.Provider) (SetupCatalog, error) {
	if !provider.Valid() {
		return SetupCatalog{}, fmt.Errorf("unsupported agent CLI %q", provider)
	}
	available := map[agentcli.Provider]bool{provider: true}
	for _, candidate := range installed {
		if candidate.Valid() {
			available[candidate] = true
		}
	}
	base, err := parseSetupProfiles(provider)
	if err != nil {
		return SetupCatalog{}, err
	}
	catalog := SetupCatalog{Models: map[string]config.Profile{}, Roles: map[string]config.RoleModels{}}
	mergeModels := func(document setupProfileDocument) {
		for name, profile := range document.Models {
			if available[agentcli.Resolve(profile.Provider, profile.Cmd)] {
				catalog.Models[name] = profile
			}
		}
	}
	mergeModels(base)
	for _, candidate := range agentcli.Supported {
		if candidate == provider || !available[candidate] {
			continue
		}
		document, err := parseSetupProfiles(candidate)
		if err != nil {
			return SetupCatalog{}, err
		}
		mergeModels(document)
	}
	keys := make([]string, 0, len(catalog.Models))
	for name := range catalog.Models {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, role := range config.AllRoles {
		configured := base.Roles[role]
		filtered := make([]string, 0, len(configured.Models))
		for _, name := range configured.Models {
			if _, ok := catalog.Models[name]; ok {
				filtered = append(filtered, name)
			}
		}
		if len(filtered) == 0 && len(keys) > 0 {
			filtered = []string{keys[0]}
		}
		configured.Models = filtered
		catalog.Roles[role] = configured
	}
	return catalog, nil
}

func parseSetupProfiles(provider agentcli.Provider) (setupProfileDocument, error) {
	source := map[agentcli.Provider]string{
		agentcli.Claude: claudeProfiles,
		agentcli.Codex:  codexProfiles,
		agentcli.Gemini: geminiProfiles,
	}[provider]
	var document setupProfileDocument
	if err := yaml.Unmarshal([]byte(source), &document); err != nil {
		return setupProfileDocument{}, fmt.Errorf("parse %s setup profiles: %w", provider, err)
	}
	return document, nil
}

func applySetupOptions(raw string, options SetupOptions) (string, error) {
	if len(options.Models) == 0 && len(options.Roles) == 0 && len(options.Plugins) == 0 {
		return raw, nil
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		return "", err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("generated setup config must be a mapping")
	}
	root := document.Content[0]
	if len(options.Models) > 0 {
		mergeSetupMapping(root, "models", options.Models)
	}
	if len(options.Roles) > 0 {
		setSetupRoleMapping(root, options.Roles)
	}
	if len(options.Plugins) > 0 {
		plugins := mappingNodeValue(root, "plugins")
		if plugins == nil || plugins.Kind != yaml.MappingNode {
			return "", fmt.Errorf("generated setup plugins must be a mapping")
		}
		mergeSetupMapping(plugins, "installed", options.Plugins)
	}
	out, err := yamlformat.EncodePreservingBlankLines([]byte(raw), root, 2)
	return string(out), err
}

func mergeSetupMapping[T any](root *yaml.Node, key string, values map[string]T) {
	mapping := mappingNodeValue(root, key)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		setSetupMapping(root, key, values)
		return
	}
	keys := make([]string, 0, len(values))
	for name := range values {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		var value yaml.Node
		_ = value.Encode(values[name])
		removeNullSetupFields(&value)
		setSetupNode(mapping, name, &value)
	}
}

func setSetupRoleMapping(root *yaml.Node, roles map[string]config.RoleModels) {
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, role := range config.AllRoles {
		value, ok := roles[role]
		if !ok {
			continue
		}
		var node yaml.Node
		_ = node.Encode(value)
		mapping.Content = append(mapping.Content, yamlScalar(role), &node)
	}
	setSetupNode(root, "roles", mapping)
}

func setSetupMapping[T any](root *yaml.Node, key string, values map[string]T) {
	keys := make([]string, 0, len(values))
	for name := range values {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, name := range keys {
		var value yaml.Node
		_ = value.Encode(values[name])
		removeNullSetupFields(&value)
		mapping.Content = append(mapping.Content, yamlScalar(name), &value)
	}
	setSetupNode(root, key, mapping)
}

func removeNullSetupFields(node *yaml.Node) {
	if node.Kind == yaml.MappingNode {
		filtered := node.Content[:0]
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i+1].Tag == "!!null" {
				continue
			}
			removeNullSetupFields(node.Content[i+1])
			filtered = append(filtered, node.Content[i], node.Content[i+1])
		}
		node.Content = filtered
	}
}

func setSetupNode(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, yamlScalar(key), value)
}

func yamlScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}
