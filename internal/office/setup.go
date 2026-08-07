package office

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

// DefaultConfig is the omo.yaml written by Setup. It is deliberately
// complete rather than minimal: every knob is present and commented, so the
// file doubles as the configuration reference.
const DefaultConfig = `# one-man-office configuration.
# Run 'omo' in this directory to start the office.

# Repositories agents may work in, as <key>: <absolute path>.
# Developer jobs name one of these keys; omo creates a git worktree per job.
# An office is either one repository, or a directory holding several of them
# (a microservice landscape) — both are supported, and 'omo setup' fills this
# in from what it found.
%s

# Runner profiles. A profile is just a command line — omo has no concept of
# a "model". 'selectable: false' hides a profile from the CEO's --model flag
# while still allowing a role to run on it.
models:
  fable:
    cmd: claude
    args: ["--model", "fable", "--dangerously-skip-permissions"]
    selectable: false
  opus:
    cmd: claude
    args: ["--model", "opus", "--dangerously-skip-permissions"]
  sonnet:
    cmd: claude
    args: ["--model", "sonnet", "--dangerously-skip-permissions"]
  haiku:
    cmd: claude
    args: ["--model", "haiku", "--dangerously-skip-permissions"]

# Default profile per role. All seven roles are required.
roles:
  ceo: fable
  product_manager: opus
  developer: sonnet
  reviewer: opus
  freelancer: sonnet
  smokealarm: haiku
  firefighter: opus

# Concurrency caps.
limits:
  max_developers: 4
  max_freelancers: 2

# The smoke alarm inspects every living agent on this interval.
smokealarm:
  interval: 5m
  tail_lines: 120

# Session transcripts in .omo/logs. Live sessions rotate at max_size_kb.
# keep counts completed session groups; living agents never count. Set -1
# to disable inactive-log pruning, or 0 to remove completed logs.
logs:
  max_size_kb: 2048
  keep: 50

# After this many consecutive rejected reviews, ask the PM to decide whether
# the findings have become nitpicking or moved out of scope.
reviews:
  escalate_after: 2

# Repeat unread-mail nudges, but never inject one while the user is actively
# typing into that agent's terminal.
notifications:
  repeat_interval: 3m
  input_debounce: 30s

# Pre-accept Claude Code's "do you trust this folder?" dialog for each
# agent's working directory. Agents have nobody to answer it, so leaving
# this off means they hang on first run in a fresh worktree.
trust_workdirs: true
`

const officeGitignore = `*
!.gitignore
`

// Setup scaffolds a new office in dir: the config, the ignored .omo layout, an
// initialised (empty) database and the editable message and prompt templates.
// The config is the initialization marker: if it already exists, Setup does
// nothing at all. Use UpdateTemplates to deliberately refresh the templates.
func Setup(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(abs, ConfigPath)
	if _, err := os.Stat(cfgPath); err == nil {
		return nil, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	var created []string

	for _, sub := range []string{".omo", ".omo/logs", ".omo/worktrees"} {
		if err := os.MkdirAll(filepath.Join(abs, sub), 0o755); err != nil {
			return nil, err
		}
	}
	ignorePath := filepath.Join(abs, ".omo", ".gitignore")
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(ignorePath, []byte(officeGitignore), 0o644); err != nil {
			return nil, err
		}
		created = append(created, ".omo/.gitignore")
	} else if err != nil {
		return nil, err
	}

	repos, layout := DiscoverRepos(abs)
	if err := os.WriteFile(cfgPath, []byte(renderConfig(repos)), 0o644); err != nil {
		return nil, err
	}
	created = append(created, fmt.Sprintf("%s (%s, %d repo(s) found)", ConfigPath, layout, len(repos)))

	// Creating the database here means a fresh office is immediately
	// inspectable (and any schema problem surfaces now, not at first spawn).
	dbPath := filepath.Join(abs, ".omo", "omo.db")
	_, dbMissing := os.Stat(dbPath)
	d, err := db.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("initialise database: %w", err)
	}
	d.Close()
	if os.IsNotExist(dbMissing) {
		created = append(created, ".omo/omo.db")
	}

	before := countFiles(filepath.Join(abs, messages.Dir))
	if err := messages.WriteDefaults(abs); err != nil {
		return nil, err
	}
	if n := countFiles(filepath.Join(abs, messages.Dir)) - before; n > 0 {
		created = append(created, fmt.Sprintf("%s/ (%d templates)", messages.Dir, n))
	}
	before = countFiles(filepath.Join(abs, prompts.Dir))
	if err := prompts.WriteDefaults(abs); err != nil {
		return nil, err
	}
	if n := countFiles(filepath.Join(abs, prompts.Dir)) - before; n > 0 {
		created = append(created, fmt.Sprintf("%s/ (%d templates)", prompts.Dir, n))
	}
	return created, nil
}

// UpdateTemplates replaces only .omo/messages and .omo/prompts with the
// defaults embedded in this binary. Both replacements are staged before the
// first live directory is moved, and a failed swap restores the old folders.
func UpdateTemplates(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, ConfigPath)); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("office is not set up in %s; run 'omo setup' first", abs)
		}
		return nil, err
	}

	omoDir := filepath.Join(abs, ".omo")
	stage, err := os.MkdirTemp(omoDir, ".setup-update-")
	if err != nil {
		return nil, fmt.Errorf("stage template update: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := messages.WriteDefaults(stage); err != nil {
		return nil, fmt.Errorf("stage message templates: %w", err)
	}
	if err := prompts.WriteDefaults(stage); err != nil {
		return nil, fmt.Errorf("stage role prompts: %w", err)
	}

	type replacement struct {
		target, fresh, backup string
		oldMoved              bool
		installed             bool
	}
	replacements := []replacement{
		{
			target: filepath.Join(abs, messages.Dir),
			fresh:  filepath.Join(stage, messages.Dir),
			backup: filepath.Join(stage, "previous-messages"),
		},
		{
			target: filepath.Join(abs, prompts.Dir),
			fresh:  filepath.Join(stage, prompts.Dir),
			backup: filepath.Join(stage, "previous-prompts"),
		},
	}

	rollback := func(last int) error {
		var errs []error
		for i := last; i >= 0; i-- {
			r := &replacements[i]
			if r.installed {
				if err := os.RemoveAll(r.target); err != nil {
					errs = append(errs, err)
					continue
				}
			}
			if r.oldMoved {
				if err := os.Rename(r.backup, r.target); err != nil {
					errs = append(errs, err)
				}
			}
		}
		return errors.Join(errs...)
	}

	for i := range replacements {
		r := &replacements[i]
		if _, err := os.Lstat(r.target); err == nil {
			if err := os.Rename(r.target, r.backup); err != nil {
				return nil, errors.Join(fmt.Errorf("back up %s: %w", r.target, err), rollback(i-1))
			}
			r.oldMoved = true
		} else if !os.IsNotExist(err) {
			return nil, errors.Join(fmt.Errorf("inspect %s: %w", r.target, err), rollback(i-1))
		}
		if err := os.Rename(r.fresh, r.target); err != nil {
			return nil, errors.Join(fmt.Errorf("install %s: %w", r.target, err), rollback(i))
		}
		r.installed = true
	}

	return []string{
		fmt.Sprintf("%s/ (%d templates)", messages.Dir, len(messages.Names)),
		fmt.Sprintf("%s/ (%d prompts)", prompts.Dir, len(prompts.Roles)+1),
	}, nil
}

func countFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	return len(entries)
}

// renderConfig fills the repos block of the default config from discovery.
func renderConfig(repos map[string]string) string {
	block := "repos: {}\n  # api: /home/you/workspace/acme/api\n  # ui:  /home/you/workspace/acme/ui"
	if len(repos) > 0 {
		var b strings.Builder
		b.WriteString("repos:")
		for _, k := range SortedKeys(repos) {
			fmt.Fprintf(&b, "\n  %s: %s", k, repos[k])
		}
		block = b.String()
	}
	return fmt.Sprintf(DefaultConfig, block)
}
