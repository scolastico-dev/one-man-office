package mcpclients

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Store is what the take-over needs of omo's settings: the servers omo
// relays, and a guarded write of one (rev api.NoRevision creates only, so
// a server of that name that appeared meanwhile is refused).
type Store interface {
	MCPServers() []api.MCPServer
	PutMCPServerIf(rev string, s api.MCPServer) error
}

// namePattern is the form of a server name in omo.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)

// maxName is the longest server name.
const maxName = 24

// hashHex is the first n hex digits of the SHA-256 of v as JSON.
func hashHex(v any, n int) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:n]
}

// configKey identifies what a server is, without its secrets: the transport
// with the command, arguments and folder, or the address without a
// trailing slash.
func configKey(s api.MCPServer) string {
	if s.Transport == api.MCPTransportStdio {
		return hashHex([]any{s.Transport, s.Command, s.Args, s.Cwd}, 16)
	}
	return hashHex([]any{s.Transport, strings.TrimRight(s.URL, "/")}, 16)
}

// secretKey identifies the values a server is reached with.
func secretKey(s api.MCPServer) string {
	return hashHex([]any{s.Env, s.Headers, s.HeadersCommand, s.OAuth}, 16)
}

// normalizeName makes an entry name a valid server name: lower case,
// [a-z0-9-], starting with a letter, at most 24 characters.
func normalizeName(n string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(n) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" || out[0] < 'a' || out[0] > 'z' {
		out = "mcp-" + out
	}
	if len(out) > maxName {
		out = out[:maxName]
	}
	return strings.TrimRight(out, "-")
}

// withSuffix is name with "-<suffix>", cut so that it stays a valid name.
func withSuffix(name, suffix string) string {
	keep := maxName - len(suffix) - 1
	if len(name) > keep {
		name = strings.TrimRight(name[:keep], "-")
	}
	return name + "-" + suffix
}

// Takeover builds the preview of a take-over from the targets' states (with
// their values, as Inspect gives them) and the servers omo has. Entries of
// the same server with the same values in several files become one
// candidate; with other values a separate one. A name omo or an earlier
// candidate uses gets the program's name as suffix; a server omo already
// has, or a name that stays taken, is a conflict. Every candidate is
// global. Entries omo cannot take over and unreadable files are in Stays.
func Takeover(states []api.MCPClientState, existing []api.MCPServer) api.MCPTakeover {
	type group struct {
		cand      api.MCPTakeoverCandidate
		configKey string
		secretKey string
		cli       string
		labels    []string
	}
	out := api.MCPTakeover{Candidates: []api.MCPTakeoverCandidate{}, Stays: []api.MCPTakeoverStay{}}
	var groups []*group
	for _, st := range states {
		if st.Error != "" {
			out.Stays = append(out.Stays, api.MCPTakeoverStay{Target: st.Target.ID, Reason: st.Error})
			continue
		}
		label := targetLabel(st.Target)
		for _, e := range st.Entries {
			if e.Reason != "" {
				out.Stays = append(out.Stays, api.MCPTakeoverStay{Target: st.Target.ID, EntryName: e.Name, Reason: e.Reason})
				continue
			}
			src := api.MCPTakeoverSource{Target: st.Target.ID, EntryName: e.Name}
			ck, sk := configKey(e.Server), secretKey(e.Server)
			i := slices.IndexFunc(groups, func(g *group) bool { return g.configKey == ck && g.secretKey == sk })
			if i >= 0 {
				g := groups[i]
				g.cand.Sources = append(g.cand.Sources, src)
				if !slices.Contains(g.labels, label) {
					g.labels = append(g.labels, label)
				}
				continue
			}
			g := &group{configKey: ck, secretKey: sk, cli: st.Target.CLI, labels: []string{label},
				cand: api.MCPTakeoverCandidate{Key: "k" + hashHex([]string{ck, sk}, 16), Name: e.Name, Server: e.Server,
					Sources: []api.MCPTakeoverSource{src}, Notes: slices.Clone(e.Notes)}}
			if slices.ContainsFunc(groups, func(o *group) bool { return o.configKey == ck }) {
				g.cand.Notes = append(g.cand.Notes, "another program has the same server with other values; it is offered separately")
			}
			groups = append(groups, g)
		}
	}
	have := map[string]string{}
	taken := map[string]bool{}
	for _, s := range existing {
		taken[s.Name] = true
		if _, ok := have[configKey(s)]; !ok {
			have[configKey(s)] = s.Name
		}
	}
	for _, g := range groups {
		c := g.cand
		base := normalizeName(c.Name)
		name := base
		if other, ok := have[g.configKey]; ok {
			c.Conflict = fmt.Sprintf("omo already has this server as %s", other)
		} else if taken[name] {
			name = withSuffix(base, g.cli)
			if taken[name] {
				c.Conflict = fmt.Sprintf("the name %s is taken in omo; choose another name", base)
			}
		}
		taken[name] = true
		c.Name = name
		c.Server.Name = name
		c.Server.Global = true
		c.Server.Source = strings.Join(g.labels, ", ")
		c.NeedsLogin = c.Server.Auth == api.MCPAuthOAuth
		out.Candidates = append(out.Candidates, c)
	}
	return out
}

func targetLabel(t api.MCPClientTarget) string {
	return Target{MCPClientTarget: t}.Label()
}

// ProjectStays lists the project-level entries of the programs in the
// given project folders (.mcp.json, .codex/config.toml,
// .agents/mcp_config.json): they stay where they are.
func ProjectStays(folders []string) []api.MCPTakeoverStay {
	files := []struct{ rel, cli, what string }{
		{".mcp.json", api.CLIClaude, "Claude's project file"},
		{filepath.Join(".codex", "config.toml"), api.CLICodex, "Codex's project file"},
		{filepath.Join(".agents", "mcp_config.json"), api.CLIAgy, "agy's project file"},
	}
	var out []api.MCPTakeoverStay
	for _, dir := range folders {
		for _, f := range files {
			path := filepath.Join(dir, f.rel)
			data, err := readConfig(path)
			if err != nil || data == nil {
				continue
			}
			fc, err := parseConfig(f.cli, data)
			if err != nil {
				continue
			}
			for _, name := range sortedKeys(fc.servers) {
				out = append(out, api.MCPTakeoverStay{Target: path, EntryName: name,
					Reason: fmt.Sprintf("it belongs to the project (%s) and stays there; omo has no per-project servers", f.what)})
			}
		}
	}
	return out
}

// Preview is the take-over preview of the targets with values as read;
// MaskTakeover masks it for an answer.
func (c *Clients) Preview(targets []Target, existing []api.MCPServer) api.MCPTakeover {
	states := make([]api.MCPClientState, len(targets))
	for i, t := range targets {
		states[i] = c.Inspect(t)
	}
	return Takeover(states, existing)
}

// ApplyTakeover takes the chosen candidates over, each in order: first omo's
// copy is stored (create only) and read back; only then, when
// req.RemoveFromCLI is set, each source file is copied into the backups
// (once per apply) and the entry is removed with the program's command. A
// candidate whose copy is not stored is removed nowhere; a failed removal
// leaves the entry in its program and says so. The candidates are built
// again from the files, so the values stored are the current ones.
func (c *Clients) ApplyTakeover(ctx context.Context, targets []Target, req api.MCPTakeoverApply, store Store) api.MCPTakeoverReport {
	report := api.MCPTakeoverReport{Results: []api.MCPTakeoverResult{}, Backups: []string{}}
	secrets := &redactor{}
	states := make([]api.MCPClientState, len(targets))
	byTarget := map[string]Target{}
	for i, t := range targets {
		in := c.inspect(t)
		secrets.values = append(secrets.values, in.secrets.values...)
		states[i] = in.state
		byTarget[t.ID] = t
	}
	preview := Takeover(states, store.MCPServers())
	byKey := map[string]api.MCPTakeoverCandidate{}
	for _, cand := range preview.Candidates {
		byKey[cand.Key] = cand
	}
	backupDir := ""
	backedUp := map[string]bool{}
	names := map[string]bool{}
	for _, pick := range req.Take {
		res := api.MCPTakeoverResult{Name: pick.Name, Removed: []api.MCPTakeoverRemoval{}}
		cand, ok := byKey[pick.Key]
		switch {
		case !ok:
			res.Error = "this server is no longer in the programs' files as it was shown; review again"
		case !namePattern.MatchString(pick.Name):
			res.Error = "the name must be 1 to 24 characters of a-z, 0-9 and -, starting with a letter"
		case names[pick.Name]:
			res.Error = "the name is chosen twice"
		}
		if res.Error != "" {
			report.Results = append(report.Results, res)
			continue
		}
		names[pick.Name] = true
		srv := cand.Server
		srv.Name, srv.Global, srv.Revision = pick.Name, true, ""
		res.NeedsLogin = cand.NeedsLogin
		if err := store.PutMCPServerIf(api.NoRevision, srv); err != nil {
			res.Error = "omo's copy was not stored, so the entry stays in the program: " + secrets.redact(err.Error())
			report.Results = append(report.Results, res)
			continue
		}
		if !slices.ContainsFunc(store.MCPServers(), func(s api.MCPServer) bool { return s.Name == pick.Name }) {
			res.Error = "omo's copy could not be read back, so the entry stays in the program"
			report.Results = append(report.Results, res)
			continue
		}
		res.Stored = true
		if req.RemoveFromCLI {
			for _, src := range cand.Sources {
				t := byTarget[src.Target]
				rm := api.MCPTakeoverRemoval{Target: src.Target}
				if !backedUp[t.ConfigPath] {
					var err error
					if backupDir == "" {
						backupDir, err = c.newBackupFolder("mcp-takeover")
					}
					var path string
					if err == nil {
						path, err = copyInto(backupDir, t.ID+"-"+filepath.Base(t.ConfigPath), t.ConfigPath)
					}
					if err != nil {
						backupDir = ""
						rm.Error = fmt.Sprintf("still in %s: no copy of its file could be made (%s), so nothing was removed; remove it by hand or retry", t.Label(), shortError(err))
						res.Removed = append(res.Removed, rm)
						continue
					}
					backedUp[t.ConfigPath] = true
					if path != "" {
						report.Backups = append(report.Backups, path)
					}
				}
				if err := c.removeEntry(ctx, t, src.EntryName, secrets); err != nil {
					rm.Error = fmt.Sprintf("still in %s: %v; remove it by hand or retry", t.Label(), err)
				} else {
					rm.Done = true
				}
				res.Removed = append(res.Removed, rm)
			}
		}
		report.Results = append(report.Results, res)
	}
	return report
}

func (c *Clients) removeEntry(ctx context.Context, t Target, entry string, secrets *redactor) error {
	if strings.HasPrefix(entry, "-") {
		return fmt.Errorf("the name starts with '-'")
	}
	if err := c.run(ctx, t, removeCommand(t, entry), secrets); err != nil {
		return err
	}
	after := c.inspect(t).state
	if after.Error != "" {
		return fmt.Errorf("the file cannot be read after the removal")
	}
	if entry == EntryName && after.State != api.MCPClientAbsent ||
		slices.ContainsFunc(after.Entries, func(e api.MCPClientEntry) bool { return e.Name == entry && !isProjectStay(e) }) {
		return fmt.Errorf("%s reported success, but the entry is still in its file", t.Program)
	}
	return nil
}

// isProjectStay says whether an entry is one of Claude's project entries.
func isProjectStay(e api.MCPClientEntry) bool {
	return strings.HasPrefix(e.Reason, "it belongs to the project")
}
