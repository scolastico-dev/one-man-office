// Package mcpclients registers omo's MCP entry in the agent programs'
// user-level configuration (claude, codex and agy) and takes over the MCP
// servers those programs have there.
//
// omo reads the programs' files but never writes them: every change goes
// through the program's own "mcp add" and "mcp remove" commands, after a
// dated copy of the file was made in omo's home. Values read from the files
// (environment, headers, client secrets) are never logged; previews show
// them masked (Mask*).
package mcpclients

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// The agent programs whose configuration omo manages.
var programs = []string{api.CLIClaude, api.CLICodex, api.CLIAgy}

// EntryName is the name of omo's own entry in the programs' files.
const EntryName = "omo"

// maxConfigFile bounds a program's configuration file (Claude's
// ~/.claude.json grows with its history).
const maxConfigFile = 16 << 20

// DefaultTimeout bounds one command of an agent program.
const DefaultTimeout = 30 * time.Second

// Options are what the package needs from the machine. Tests give a
// temporary home, fake programs and their own environment.
type Options struct {
	// Home is the OS user's home folder: the programs' default files are
	// below it.
	Home string
	// OmoPath is the absolute path of the omo executable the bridge entry
	// runs.
	OmoPath string
	// OmoHome is omo's home; it is passed to the bridge as OMO_HOME when
	// PassHome is set (the home is not the default one).
	OmoHome  string
	PassHome bool
	// Env is the environment of the programs' commands
	// (childenv.Inherited()). The account folder variable is set or removed
	// per target.
	Env []string
	// LookPath finds a program; nil is exec.LookPath.
	LookPath func(string) (string, error)
	// Getenv reads omo's own environment, for the notes on ${VAR}
	// references; nil says every variable is unset.
	Getenv func(string) (string, bool)
	// Backups is the folder that gets the dated copies of the files.
	Backups string
	// Now is the clock of the backup names; nil is time.Now.
	Now func() time.Time
	// Timeout bounds one command; 0 is DefaultTimeout.
	Timeout time.Duration
}

// Clients inspects and changes the agent programs' MCP configuration.
type Clients struct{ o Options }

// New returns Clients working with o.
func New(o Options) *Clients {
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Getenv == nil {
		o.Getenv = func(string) (string, bool) { return "", false }
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	return &Clients{o: o}
}

// Target is one configuration file of an agent program, with the command
// that changes it.
type Target struct {
	api.MCPClientTarget
	// Program is what runs the agent program: the command of a profile of
	// the target, else the program's name.
	Program string
}

// Label names the target in a server's source and in messages: "claude",
// or "claude@<account folder>".
func (t Target) Label() string {
	if t.Account == "" {
		return t.CLI
	}
	return t.CLI + "@" + t.Account
}

// accountVariable is the variable that moves a program's configuration
// folder ("" for agy, which has none).
func accountVariable(cli string) string {
	switch cli {
	case api.CLIClaude:
		return "CLAUDE_CONFIG_DIR"
	case api.CLICodex:
		return "CODEX_HOME"
	}
	return ""
}

// configPath is the user-level MCP file of cli for an account folder ("" is
// the program's default).
func configPath(cli, home, account string) string {
	switch cli {
	case api.CLIClaude:
		if account != "" {
			return filepath.Join(account, ".claude.json")
		}
		return filepath.Join(home, ".claude.json")
	case api.CLICodex:
		if account != "" {
			return filepath.Join(account, "config.toml")
		}
		return filepath.Join(home, ".codex", "config.toml")
	case api.CLIAgy:
		return filepath.Join(home, ".gemini", "config", "mcp_config.json")
	}
	return ""
}

// short is the first 8 hex digits of the SHA-256 of s.
func short(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// expandHome expands a leading ~ of dir with home.
func expandHome(dir, home string) string {
	if dir == "~" {
		return home
	}
	if strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`) {
		return filepath.Join(home, dir[2:])
	}
	return dir
}

// Targets lists the files omo manages for the profiles: per program its
// default file (when a profile of the program uses no account folder, or the
// file exists) and the file of every distinct account folder of its
// profiles. agy has no account folders: only its default file.
func Targets(profiles []api.Profile, home string) []Target {
	var out []Target
	for _, cli := range programs {
		var defProgram string
		useDefault := false
		accounts := map[string]string{}
		var order []string
		for _, p := range profiles {
			if p.CLI != cli {
				continue
			}
			program := p.Command
			acc := ""
			if accountVariable(cli) != "" && p.AccountDir != "" {
				acc = filepath.Clean(expandHome(p.AccountDir, home))
				if !filepath.IsAbs(acc) {
					continue
				}
			}
			if acc == "" {
				useDefault = true
				if defProgram == "" {
					defProgram = program
				}
				continue
			}
			if _, ok := accounts[acc]; !ok {
				order = append(order, acc)
				accounts[acc] = program
			} else if accounts[acc] == "" {
				accounts[acc] = program
			}
		}
		def := configPath(cli, home, "")
		if !useDefault {
			if _, err := os.Stat(def); err == nil {
				useDefault = true
			}
		}
		if useDefault {
			out = append(out, newTarget(cli, "", def, defProgram))
		}
		for _, acc := range order {
			out = append(out, newTarget(cli, acc, configPath(cli, home, acc), accounts[acc]))
		}
	}
	return out
}

func newTarget(cli, account, path, program string) Target {
	id := cli
	if account != "" {
		id = cli + "-" + short(account)
	}
	if program == "" {
		program = cli
	}
	return Target{MCPClientTarget: api.MCPClientTarget{ID: id, CLI: cli, Account: account, ConfigPath: path}, Program: program}
}

// envFor is the environment of a command for t: Env with the program's
// account variable set to the account folder, or removed for the default
// file, so that omo's own environment never redirects the command.
func (c *Clients) envFor(t Target) []string {
	name := accountVariable(t.CLI)
	out := make([]string, 0, len(c.o.Env)+1)
	for _, kv := range c.o.Env {
		if name != "" && strings.HasPrefix(kv, name+"=") {
			continue
		}
		out = append(out, kv)
	}
	if name != "" && t.Account != "" {
		out = append(out, name+"="+t.Account)
	}
	return out
}

// tail keeps the last bytes written to it.
type tail struct{ buf []byte }

const tailSize = 4 << 10

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailSize {
		t.buf = t.buf[len(t.buf)-tailSize:]
	}
	return len(p), nil
}

// lastLine is the last non-empty line of out, at most 300 characters.
func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if r := []rune(line); len(r) > 300 {
		line = string(r[:300]) + "…"
	}
	return line
}

// run runs argv for t without a terminal, bounded by the timeout. A failure
// names the program, its exit and the last line it printed, with every
// value of secrets masked.
func (c *Clients) run(ctx context.Context, t Target, argv []string, secrets *redactor) error {
	path, err := c.o.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("the program %s was not found", argv[0])
	}
	ctx, cancel := context.WithTimeout(ctx, c.o.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, argv[1:]...)
	cmd.Env = c.envFor(t)
	cmd.Stdin = nil
	var out tail
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s did not finish within %s", strings.Join(argv[:min(len(argv), 3)], " "), c.o.Timeout)
	}
	msg := fmt.Sprintf("%s failed (%v)", strings.Join(argv[:min(len(argv), 3)], " "), err)
	if line := lastLine(out.buf); line != "" {
		msg += ": " + line
	}
	return errors.New(secrets.redact(msg))
}

// readConfig reads a program's file. A missing file gives nil data and no
// error.
func readConfig(path string) ([]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxConfigFile {
		return nil, errors.New("is larger than 16 MiB")
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}

// newBackupFolder creates <Backups>/<time>-<what>, with -2, -3 and so on
// when that name is taken.
func (c *Clients) newBackupFolder(what string) (string, error) {
	if c.o.Backups == "" {
		return "", errors.New("no backup folder is configured")
	}
	if err := os.MkdirAll(c.o.Backups, 0o700); err != nil {
		return "", err
	}
	stamp := c.o.Now().Local().Format("2006-01-02-150405") + "-" + what
	for i := 1; i < 100; i++ {
		name := stamp
		if i > 1 {
			name = fmt.Sprintf("%s-%d", stamp, i)
		}
		dir := filepath.Join(c.o.Backups, name)
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("no free backup folder name for %s", stamp)
}

// copyInto copies the file src into dir as name (0600) and returns the
// copy's path; a missing src gives "".
func copyInto(dir, name, src string) (string, error) {
	data, err := readConfig(src)
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", nil
	}
	dst := filepath.Join(dir, name)
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return dst, nil
}

// backupWhat is the folder name part of a backup of t's file.
func backupWhat(t Target) string {
	if t.Account == "" {
		return "mcp-" + t.CLI
	}
	return "mcp-" + t.CLI + "-" + short(t.Account)
}

// removeCommand is the command that removes entry from t's file.
func removeCommand(t Target, entry string) []string {
	if t.CLI == api.CLIClaude {
		return []string{t.Program, "mcp", "remove", "-s", "user", entry}
	}
	return []string{t.Program, "mcp", "remove", entry}
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
