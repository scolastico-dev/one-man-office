package office

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/yamlformat"
	"gopkg.in/yaml.v3"
)

// EnableGitIntegration upgrades an office to the file-based Git handoff mode.
// Configuration paths become relative to the office root and only durable
// handoff assets remain visible to Git; runtime state stays ignored.
func EnableGitIntegration(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(abs, ConfigPath)
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	if err := rewriteGitConfig(path, abs, cfg); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(abs, ".omo", ".gitignore"), []byte(officeGitIntegrationGitignore), 0o644); err != nil {
		return nil, err
	}

	git := gitops.New()
	for _, configured := range cfg.Repos {
		repo := configured.Path
		if within(abs, repo) {
			rel, relErr := filepath.Rel(repo, filepath.Join(abs, ".omo"))
			if relErr == nil {
				if relErr = git.Allow(repo, "/"+filepath.ToSlash(rel)+"/"); relErr != nil {
					return nil, relErr
				}
			}
		}
	}
	gitRoot, inWorktree, err := containingGitWorktree(abs)
	if err != nil {
		return nil, err
	}
	if inWorktree {
		visiblePath, err := filepath.Rel(gitRoot, filepath.Join(abs, ".omo", "omo.yaml"))
		if err != nil {
			return nil, err
		}
		visiblePath = filepath.ToSlash(visiblePath)
		check := exec.Command("git", "-C", gitRoot, "check-ignore", "-q", "--no-index", "--", visiblePath)
		if checkErr := check.Run(); checkErr == nil {
			out, _ := exec.Command("git", "-C", gitRoot, "check-ignore", "-v", "--no-index", "--", visiblePath).CombinedOutput()
			return nil, fmt.Errorf("%s remains ignored by a user-owned Git rule (%s); remove or narrow that rule, then rerun 'omo setup --with-git'", visiblePath, strings.TrimSpace(string(out)))
		} else if exit, ok := checkErr.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("check Git visibility for %s: %w", visiblePath, checkErr)
		}
		cmd := exec.Command("git", "-C", gitRoot, "config", "--local", "omo.gitIntegration", "true")
		if out, runErr := cmd.CombinedOutput(); runErr != nil {
			return nil, fmt.Errorf("configure Git integration: %w: %s", runErr, strings.TrimSpace(string(out)))
		}
	}
	return []string{ConfigPath, ".omo/.gitignore"}, nil
}

func containingGitWorktree(dir string) (string, bool, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("find containing Git worktree: %w", err)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false, fmt.Errorf("find containing Git worktree: Git returned an empty root")
	}
	return filepath.Clean(root), true, nil
}

func rewriteGitConfig(path, office string, cfg *config.Config) error {
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
	if repos != nil && repos.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(repos.Content); i += 2 {
			name := repos.Content[i].Value
			absolute := cfg.Repos[name].Path
			if absolute == "" {
				continue
			}
			rel, err := filepath.Rel(office, absolute)
			if err != nil {
				return err
			}
			repos.Content[i+1].Value = filepath.ToSlash(rel)
			repos.Content[i+1].Style = 0
		}
	}
	gitIntegration := mappingValue(root, "git_integration")
	if gitIntegration == nil {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "git_integration"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	} else {
		gitIntegration.Kind = yaml.ScalarNode
		gitIntegration.Tag = "!!bool"
		gitIntegration.Value = "true"
		gitIntegration.Content = nil
	}
	out, err := yamlformat.EncodePreservingBlankLines(raw, root, 2)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".omo-git-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}
