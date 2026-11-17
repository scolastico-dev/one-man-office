package mcpclients

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// bridgeArgs are the arguments of omo's bridge entry.
var bridgeArgs = []string{"mcp", "stdio"}

// inspection is one read of a target's file.
type inspection struct {
	state api.MCPClientState
	// omo is omo's entry as read; nil when there is none.
	omo any
	// secrets holds every secret value of the file's entries.
	secrets *redactor
}

// Inspect reads t's file: omo's entry and its state, and the other entries
// mapped to omo's server settings. Values are as read; MaskState masks them
// for an answer. A file that cannot be read gives the state absent and the
// reason in Error.
func (c *Clients) Inspect(t Target) api.MCPClientState { return c.inspect(t).state }

func (c *Clients) inspect(t Target) inspection {
	in := inspection{
		state:   api.MCPClientState{Target: t.MCPClientTarget, State: api.MCPClientAbsent, Entry: json.RawMessage("null"), Entries: []api.MCPClientEntry{}},
		secrets: &redactor{},
	}
	data, err := readConfig(t.ConfigPath)
	if err != nil {
		in.state.Error = fmt.Sprintf("%s cannot be read: %s", t.ConfigPath, shortError(err))
		return in
	}
	if data == nil {
		return in
	}
	fc, err := parseConfig(t.CLI, data)
	if err != nil {
		in.state.Error = fmt.Sprintf("%s cannot be read: %v", t.ConfigPath, err)
		return in
	}
	in.secrets.addRaw(fc.servers)
	for _, name := range sortedKeys(fc.servers) {
		v := fc.servers[name]
		if name == EntryName {
			in.omo = v
			if raw, err := json.Marshal(v); err == nil {
				in.state.Entry = raw
			}
			in.state.State = c.classify(t.CLI, v)
			continue
		}
		in.state.Entries = append(in.state.Entries, c.mapEntry(t.CLI, name, v))
	}
	for _, p := range fc.projects {
		in.state.Entries = append(in.state.Entries, api.MCPClientEntry{Name: p.name, Notes: []string{},
			Reason: fmt.Sprintf("it belongs to the project %s (Claude's local scope) and stays there; omo has no per-project servers", p.folder)})
	}
	return in
}

// shortError is an error of reading a file without its path.
func shortError(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// bridgeEnv is the environment of omo's bridge entry.
func (c *Clients) bridgeEnv() map[string]string {
	if c.o.PassHome && c.o.OmoHome != "" {
		return map[string]string{"OMO_HOME": c.o.OmoHome}
	}
	return nil
}

// bridgeEntry is omo's entry as the program writes it.
func (c *Clients) bridgeEntry(cli string) map[string]any {
	e := map[string]any{"command": c.o.OmoPath, "args": bridgeArgs}
	if cli == api.CLIClaude {
		e["type"] = "stdio"
	}
	if env := c.bridgeEnv(); env != nil {
		e["env"] = env
	}
	return e
}

// classify says whether an omo entry is omo's current bridge (installed), a
// bridge with another path or environment (outdated) or something else
// (foreign).
func (c *Clients) classify(cli string, v any) string {
	raw, ok := v.(map[string]any)
	if !ok {
		return api.MCPClientForeign
	}
	command := str(raw["command"])
	args, _ := strs(raw["args"])
	typ := str(raw["type"])
	if typ != "" && typ != "stdio" || raw["url"] != nil || raw["serverUrl"] != nil || !slices.Equal(args, bridgeArgs) {
		return api.MCPClientForeign
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(filepath.FromSlash(command))), ".exe")
	if base != "omo" {
		return api.MCPClientForeign
	}
	env, _ := strMap(raw["env"])
	if command == c.o.OmoPath && maps.Equal(env, c.bridgeEnv()) {
		return api.MCPClientInstalled
	}
	return api.MCPClientOutdated
}

// installCommands are the commands that add omo's bridge entry to t.
func (c *Clients) installCommand(t Target) []string {
	env := c.bridgeEnv()
	switch t.CLI {
	case api.CLIClaude:
		data, _ := json.Marshal(c.bridgeEntry(t.CLI))
		return []string{t.Program, "mcp", "add-json", "-s", "user", EntryName, string(data)}
	case api.CLICodex:
		argv := []string{t.Program, "mcp", "add", EntryName}
		for _, k := range sortedKeys(env) {
			argv = append(argv, "--env", k+"="+env[k])
		}
		return append(append(argv, "--", c.o.OmoPath), bridgeArgs...)
	default:
		argv := []string{t.Program, "mcp", "add"}
		for _, k := range sortedKeys(env) {
			argv = append(argv, "-e", k+"="+env[k])
		}
		return append(append(argv, EntryName, c.o.OmoPath), bridgeArgs...)
	}
}

// Plan says what installing or removing omo's entry in t would run, and
// how the entry changes. Values of the entry are masked.
func (c *Clients) Plan(t Target, action string) (api.MCPClientPlan, error) {
	plan, _, err := c.plan(t, action)
	return plan, err
}

func (c *Clients) plan(t Target, action string) (api.MCPClientPlan, inspection, error) {
	if action != api.MCPClientInstall && action != api.MCPClientRemove {
		return api.MCPClientPlan{}, inspection{}, api.Errorf(api.CodeBadRequest, "action must be %s or %s", api.MCPClientInstall, api.MCPClientRemove)
	}
	in := c.inspect(t)
	if in.state.Error != "" {
		return api.MCPClientPlan{}, in, api.Errorf(api.CodeConflict, "%s", in.state.Error)
	}
	if action == api.MCPClientInstall && c.o.OmoPath == "" {
		return api.MCPClientPlan{}, in, api.Errorf(api.CodeUnavailable, "omo cannot find its own executable")
	}
	p := api.MCPClientPlan{Target: t.MCPClientTarget, Action: action, File: t.ConfigPath, Commands: [][]string{},
		Before: maskRaw(in.state.Entry), After: maskRaw(in.state.Entry)}
	var msg []string
	st := in.state.State
	switch action {
	case api.MCPClientInstall:
		switch st {
		case api.MCPClientInstalled:
			msg = append(msg, "omo is already installed in "+t.ConfigPath+".")
		case api.MCPClientAbsent:
			p.Commands = [][]string{c.installCommand(t)}
		case api.MCPClientOutdated, api.MCPClientForeign:
			if st == api.MCPClientForeign {
				msg = append(msg, "The file has an entry named omo that is not omo's bridge; it is replaced.")
			} else {
				msg = append(msg, "The file has omo's bridge with another path or environment; it is replaced.")
			}
			if t.CLI == api.CLIAgy {
				p.Commands = [][]string{c.installCommand(t)}
			} else {
				p.Commands = [][]string{removeCommand(t, EntryName), c.installCommand(t)}
			}
		}
		if len(p.Commands) > 0 {
			after, _ := json.Marshal(c.bridgeEntry(t.CLI))
			p.After = maskRaw(after)
		}
	case api.MCPClientRemove:
		if st == api.MCPClientAbsent {
			msg = append(msg, "omo is not installed in "+t.ConfigPath+".")
			break
		}
		if st == api.MCPClientForeign {
			msg = append(msg, "The entry named omo is not omo's bridge; it is removed all the same.")
		}
		p.Commands = [][]string{removeCommand(t, EntryName)}
		p.After = json.RawMessage("null")
	}
	if len(p.Commands) > 0 {
		if v := accountVariable(t.CLI); v != "" && t.Account != "" {
			msg = append(msg, fmt.Sprintf("The commands run with %s=%s.", v, t.Account))
		}
		msg = append(msg, "A copy of the file is saved in omo's backups first.")
	}
	p.Message = strings.Join(msg, " ")
	p.Hash = planHash(p)
	return p, in, nil
}

// planHash identifies everything a plan shows.
func planHash(p api.MCPClientPlan) string {
	p.Hash = ""
	data, _ := json.Marshal(p)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Apply runs the plan of action for t when it still has the hash the user
// confirmed: first a copy of the file into the backups, then the commands
// in order. A plan without commands runs nothing. A changed file gives
// conflict; a failed command gives an *api.Error whose Details are the
// state after it and the backup.
func (c *Clients) Apply(ctx context.Context, t Target, action, hash string) (api.MCPClientApplied, error) {
	p, in, err := c.plan(t, action)
	if err != nil {
		return api.MCPClientApplied{}, err
	}
	if p.Hash != hash {
		return api.MCPClientApplied{}, api.Errorf(api.CodeConflict, "the file changed; review again")
	}
	if len(p.Commands) == 0 {
		return api.MCPClientApplied{State: in.state}, nil
	}
	dir, err := c.newBackupFolder(backupWhat(t))
	if err != nil {
		return api.MCPClientApplied{}, api.Errorf(api.CodeInternal, "no copy of %s could be made, so nothing was changed: %s", t.ConfigPath, shortError(err))
	}
	backup, err := copyInto(dir, filepath.Base(t.ConfigPath), t.ConfigPath)
	if err != nil {
		return api.MCPClientApplied{}, api.Errorf(api.CodeInternal, "no copy of %s could be made, so nothing was changed: %s", t.ConfigPath, shortError(err))
	}
	for i, argv := range p.Commands {
		if err := c.run(ctx, t, argv, in.secrets); err != nil {
			after := c.inspect(t).state
			applied := api.MCPClientApplied{State: after, Backup: backup}
			e := api.Errorf(api.CodeConflict, "step %d of %d failed: %v. omo's entry in %s is now %s.%s",
				i+1, len(p.Commands), err, t.ConfigPath, after.State, backupNote(backup))
			e.Details = MaskApplied(applied)
			return applied, e
		}
	}
	after := c.inspect(t).state
	applied := api.MCPClientApplied{State: after, Backup: backup}
	want := api.MCPClientInstalled
	if action == api.MCPClientRemove {
		want = api.MCPClientAbsent
	}
	if after.State != want {
		e := api.Errorf(api.CodeConflict, "%s reported success, but omo's entry in %s is %s.%s", t.Program, t.ConfigPath, after.State, backupNote(backup))
		e.Details = MaskApplied(applied)
		return applied, e
	}
	return applied, nil
}

func backupNote(backup string) string {
	if backup == "" {
		return " The file did not exist before."
	}
	return " The file before is saved as " + backup + "."
}

// MaskApplied masks the values of an apply's answer.
func MaskApplied(a api.MCPClientApplied) api.MCPClientApplied {
	a.State = MaskState(a.State)
	return a
}
