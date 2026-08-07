package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/office"
)

func addRepoCommands(root *cobra.Command) {
	repo := &cobra.Command{Use: "repo", Short: "List or modify the repositories available to the office"}
	repo.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured repositories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, cfg, err := loadOfficeConfig()
			if err != nil {
				return err
			}
			if len(cfg.Repos) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no repositories configured")
				return nil
			}
			for _, name := range office.SortedKeys(cfg.Repos) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", name, cfg.Repos[name])
			}
			return nil
		},
	})
	repo.AddCommand(&cobra.Command{
		Use:   "add [name] <path>",
		Short: "Add or update a repository (name defaults to the directory name)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, path := "", args[0]
			if len(args) == 2 {
				name, path = args[0], args[1]
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if name == "" {
				name = filepath.Base(abs)
			}
			name = strings.TrimSpace(name)
			if name == "" || name == "." || name == string(filepath.Separator) {
				return fmt.Errorf("repository name must not be empty")
			}
			if !office.IsRepo(abs) {
				return fmt.Errorf("%s is not a Git repository", abs)
			}
			configPath, cfg, err := loadOfficeConfig()
			if err != nil {
				return err
			}
			verb := "added"
			if _, exists := cfg.Repos[name]; exists {
				verb = "updated"
			}
			if err := editRepoConfig(configPath, name, abs, false); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s repository %s: %s\n", verb, name, abs)
			return nil
		},
	})
	repo.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a repository from the office",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, cfg, err := loadOfficeConfig()
			if err != nil {
				return err
			}
			if _, exists := cfg.Repos[args[0]]; !exists {
				return fmt.Errorf("repository %q is not configured", args[0])
			}
			if err := editRepoConfig(configPath, args[0], "", true); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed repository %s\n", args[0])
			return nil
		},
	})
	root.AddCommand(repo)
}

func loadOfficeConfig() (string, *config.Config, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, office.ConfigPath)
	cfg, err := config.Load(path)
	if os.IsNotExist(err) {
		return "", nil, fmt.Errorf("no %s in %s — run 'omo setup' first", office.ConfigPath, dir)
	}
	return path, cfg, err
}

func editRepoConfig(path, name, repoPath string, remove bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: expected a YAML mapping", path)
	}
	root := doc.Content[0]
	repos := mappingValue(root, "repos")
	if repos == nil {
		repos = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "repos"}, repos)
	}
	if repos.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: repos must be a mapping", path)
	}
	for i := 0; i < len(repos.Content); i += 2 {
		if repos.Content[i].Value != name {
			continue
		}
		if remove {
			repos.Content = append(repos.Content[:i], repos.Content[i+2:]...)
		} else {
			repos.Content[i+1].Kind = yaml.ScalarNode
			repos.Content[i+1].Tag = "!!str"
			repos.Content[i+1].Value = repoPath
		}
		return writeYAMLAtomic(path, &doc)
	}
	if remove {
		return fmt.Errorf("repository %q is not configured", name)
	}
	repos.Content = append(repos.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: repoPath})
	return writeYAMLAtomic(path, &doc)
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func writeYAMLAtomic(path string, doc *yaml.Node) error {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".omo-repos-*")
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
