// Package sandbox prepares a private agent home and a kernel-enforced launch policy.
package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrUnsupported means this host cannot enforce the requested sandbox.
var ErrUnsupported = errors.New("sandbox unsupported")

// Policy is the on-disk contract between the supervisor and __sandbox-exec.
// All paths are absolute and resolved before the launcher starts.
type Policy struct {
	ReadPaths   []string `json:"read_paths"`
	WriteDirs   []string `json:"write_dirs"`
	WriteFiles  []string `json:"write_files"`
	PrivateHome string   `json:"private_home"`
	PrivateTemp string   `json:"private_temp"`
	PTY         string   `json:"pty"`
	Socket      string   `json:"socket"`
	DeniedEnv   []string `json:"denied_env"`
}

type Options struct {
	RealHome        string
	OfficeRoot      string
	RepoPaths       []string
	HomeLinks       []string
	HomeLinkTargets map[string]string
	ReadPaths       []string
	Command         string
	WorkDir         string
	Environment     []string
	Socket          string
	TempParent      string
}

type Prepared struct {
	Policy     Policy
	PolicyPath string
	Command    string
	root       string
}

var deniedEnvironment = []string{"SSH_AUTH_SOCK", "GH_TOKEN", "GITHUB_TOKEN", "GH_CONFIG_DIR", "GIT_SSH_COMMAND", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "OMO_CONTROL_URL", "OMO_CONTROL_TOKEN"}

func canonical(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("sandbox path %q must be absolute", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve sandbox path %q: %w", path, err)
	}
	return filepath.Clean(resolved), nil
}

func appendPath(paths []string, path string) []string {
	for _, existing := range paths {
		if existing == path {
			return paths
		}
	}
	return append(paths, path)
}

func coversHome(path, home string) bool {
	rel, err := filepath.Rel(path, home)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveCommand(command, workdir string, environment []string) (string, error) {
	if command == "" {
		return "", errors.New("sandbox command is empty")
	}
	if workdir == "" {
		var err error
		workdir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(workdir) {
		return "", fmt.Errorf("sandbox workdir %q must be absolute", workdir)
	}
	check := func(path string) (string, bool) {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return "", false
		}
		resolved, err := canonical(path)
		return resolved, err == nil
	}
	if filepath.IsAbs(command) {
		if resolved, ok := check(command); ok {
			return resolved, nil
		}
		return "", fmt.Errorf("sandbox command %q is not executable", command)
	}
	if strings.ContainsRune(command, filepath.Separator) {
		if resolved, ok := check(filepath.Join(workdir, command)); ok {
			return resolved, nil
		}
		return "", fmt.Errorf("sandbox command %q is not executable", command)
	}
	pathEnv := os.Getenv("PATH")
	if environment != nil {
		pathEnv = ""
		for _, item := range environment {
			if strings.HasPrefix(item, "PATH=") {
				pathEnv = strings.TrimPrefix(item, "PATH=")
			}
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(workdir, dir)
		}
		if resolved, ok := check(filepath.Join(dir, command)); ok {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("sandbox command %q not found on session PATH", command)
}

func Prepare(o Options) (_ *Prepared, err error) {
	if err := platformCheck(); err != nil {
		return nil, err
	}
	home, err := canonical(o.RealHome)
	if err != nil {
		return nil, err
	}
	office, err := canonical(o.OfficeRoot)
	if err != nil {
		return nil, err
	}
	if coversHome(office, home) {
		return nil, fmt.Errorf("office root %q would expose the real HOME", office)
	}
	command, err := resolveCommand(o.Command, o.WorkDir, o.Environment)
	if err != nil {
		return nil, err
	}
	commandAccess := filepath.Dir(command)
	if coversHome(commandAccess, home) {
		commandAccess = command
	}
	parent := o.TempParent
	if parent == "" {
		parent = os.TempDir()
	}
	parent, err = canonical(parent)
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(parent, "omo-sandbox-")
	if err != nil {
		return nil, fmt.Errorf("create sandbox directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(root)
		}
	}()
	privateHome := filepath.Join(root, "home")
	privateTemp := filepath.Join(root, "tmp")
	for _, dir := range []string{privateHome, privateTemp} {
		if err = os.Mkdir(dir, 0700); err != nil {
			return nil, err
		}
	}
	if err = os.WriteFile(filepath.Join(privateHome, ".gitconfig"), []byte("[safe]\n\tdirectory = *\n"), 0600); err != nil {
		return nil, err
	}
	policy := Policy{PrivateHome: privateHome, PrivateTemp: privateTemp, DeniedEnv: append([]string(nil), deniedEnvironment...)}
	// Landlock rules operate on inodes. Excluding the real home parent avoids
	// granting access to other home directories through a broad root rule.
	components := strings.Split(strings.TrimPrefix(home, string(filepath.Separator)), string(filepath.Separator))
	exclude := string(filepath.Separator) + components[0]
	entries, err := os.ReadDir(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		path := filepath.Join(string(filepath.Separator), entry.Name())
		if path == exclude || path == "/root" {
			continue
		}
		resolved, resolveErr := canonical(path)
		if resolveErr != nil {
			continue
		} // broken top-level link has no accessible inode
		if coversHome(resolved, home) {
			continue
		}
		policy.ReadPaths = appendPath(policy.ReadPaths, resolved)
	}
	for _, path := range append([]string{office, commandAccess, privateHome, privateTemp}, o.RepoPaths...) {
		resolved, resolveErr := canonical(path)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if coversHome(resolved, home) {
			return nil, fmt.Errorf("sandbox path %q would expose the real HOME", resolved)
		}
		policy.ReadPaths = appendPath(policy.ReadPaths, resolved)
	}
	for _, path := range o.ReadPaths {
		resolved, resolveErr := canonical(path)
		if resolveErr != nil {
			return nil, resolveErr
		}
		policy.ReadPaths = appendPath(policy.ReadPaths, resolved)
	}
	policy.WriteDirs = []string{privateHome, privateTemp}
	for _, link := range o.HomeLinks {
		if link == "" || link == "." || link == ".." || filepath.Base(link) != link || filepath.IsAbs(link) {
			return nil, fmt.Errorf("invalid sandbox home link %q", link)
		}
		targetPath := filepath.Join(home, link)
		if override := o.HomeLinkTargets[link]; override != "" {
			targetPath = override
		}
		target, resolveErr := canonical(targetPath)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if coversHome(target, home) {
			return nil, fmt.Errorf("sandbox home link %q would expose the real HOME", link)
		}
		info, statErr := os.Stat(target)
		if statErr != nil {
			return nil, statErr
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("sandbox home link %q is not a directory", link)
		}
		if err = os.Symlink(target, filepath.Join(privateHome, link)); err != nil {
			return nil, err
		}
		policy.ReadPaths = appendPath(policy.ReadPaths, target)
		policy.WriteDirs = appendPath(policy.WriteDirs, target)
	}
	policy.WriteFiles = []string{"/dev/null"}
	if o.Socket != "" {
		policy.Socket, err = canonical(o.Socket)
		if err != nil {
			return nil, err
		}
		policy.WriteFiles = appendPath(policy.WriteFiles, policy.Socket)
	}
	sort.Strings(policy.ReadPaths)
	sort.Strings(policy.WriteDirs)
	prepared := &Prepared{Policy: policy, PolicyPath: filepath.Join(root, "policy.json"), Command: command, root: root}
	if err = prepared.save(); err != nil {
		return nil, err
	}
	return prepared, nil
}

func (p *Prepared) SetPTY(path string) error {
	resolved, err := canonical(path)
	if err != nil {
		return err
	}
	p.Policy.PTY = resolved
	p.Policy.WriteFiles = appendPath(p.Policy.WriteFiles, resolved)
	return p.save()
}

func (p *Prepared) save() error {
	data, err := json.Marshal(p.Policy)
	if err != nil {
		return err
	}
	return os.WriteFile(p.PolicyPath, data, 0600)
}

func (p *Prepared) Cleanup() {
	if p != nil && p.root != "" {
		_ = os.RemoveAll(p.root)
	}
}

func ReadPolicy(path string) (Policy, error) {
	if !filepath.IsAbs(path) {
		return Policy{}, fmt.Errorf("policy path must be absolute")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, err
	}
	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return Policy{}, err
	}
	if policy.PrivateHome == "" || policy.PrivateTemp == "" || policy.PTY == "" {
		return Policy{}, errors.New("incomplete sandbox policy")
	}
	return policy, nil
}

func Environment(env []string, p Policy) []string {
	denied := make(map[string]bool, len(p.DeniedEnv))
	for _, key := range p.DeniedEnv {
		denied[key] = true
	}
	owned := map[string]string{"HOME": p.PrivateHome, "TMPDIR": p.PrivateTemp,
		"XDG_CONFIG_HOME": filepath.Join(p.PrivateHome, ".config"), "XDG_CACHE_HOME": filepath.Join(p.PrivateHome, ".cache"),
		"XDG_DATA_HOME": filepath.Join(p.PrivateHome, ".local", "share"), "XDG_STATE_HOME": filepath.Join(p.PrivateHome, ".local", "state")}
	result := make([]string, 0, len(env)+len(owned))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if !ok || denied[key] {
			continue
		}
		if _, exists := owned[key]; exists {
			continue
		}
		result = append(result, item)
	}
	for key, value := range owned {
		result = append(result, key+"="+value)
	}
	return result
}
