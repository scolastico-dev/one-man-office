package mcpclients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// golden compares v as indented JSON, with the temporary paths replaced,
// with testdata/golden/<name>.json.
func golden(t *testing.T, f *fakes, name string, v any) {
	t.Helper()
	// Plan hashes cover the temporary paths; TestPlanHash pins them.
	switch x := v.(type) {
	case api.MCPClientPlan:
		x.Hash = ""
		v = x
	case map[string]api.MCPClientPlan:
		m := map[string]api.MCPClientPlan{}
		for k, p := range x {
			p.Hash = ""
			m[k] = p
		}
		v = m
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, r := range []struct{ from, to string }{
		{f.opts.OmoPath, "$OMO"}, {f.omoHome, "$OMO_HOME"}, {f.home, "$HOME"},
	} {
		quoted, _ := json.Marshal(r.from)
		got = strings.ReplaceAll(got, strings.Trim(string(quoted), `"`), r.to)
	}
	path := filepath.Join("testdata", "golden", name+".json")
	if *update {
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run the test with -update to write it)", err)
	}
	if got+"\n" != string(want) {
		t.Errorf("%s differs from the golden file; got:\n%s", name, got)
	}
}

func target(t *testing.T, f *fakes, cli, account string) Target {
	t.Helper()
	return newTarget(cli, account, configPath(cli, f.home, account), cli)
}

func TestTargets(t *testing.T) {
	home := t.TempDir()
	profiles := []api.Profile{
		{Name: "claude", CLI: api.CLIClaude, Command: "/opt/claude/bin/claude"},
		{Name: "work", CLI: api.CLIClaude, AccountDir: "~/accounts/work"},
		{Name: "work-opus", CLI: api.CLIClaude, AccountDir: filepath.Join(home, "accounts", "work"), Command: "claude-work"},
		{Name: "codex", CLI: api.CLICodex, AccountDir: "/srv/codex-account"},
		{Name: "agy", CLI: api.CLIAgy, AccountDir: "/ignored"},
		{Name: "gemini", CLI: api.CLIGemini},
	}
	got := Targets(profiles, home)
	type row struct{ id, cli, account, path, program string }
	var rows []row
	for _, x := range got {
		rows = append(rows, row{x.ID, x.CLI, x.Account, x.ConfigPath, x.Program})
	}
	work := filepath.Join(home, "accounts", "work")
	want := []row{
		{"claude", "claude", "", filepath.Join(home, ".claude.json"), "/opt/claude/bin/claude"},
		{"claude-" + short(work), "claude", work, filepath.Join(work, ".claude.json"), "claude-work"},
		{"codex-" + short("/srv/codex-account"), "codex", "/srv/codex-account", "/srv/codex-account/config.toml", "codex"},
		{"agy", "agy", "", filepath.Join(home, ".gemini", "config", "mcp_config.json"), "agy"},
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("targets\n%v\nwant\n%v", rows, want)
	}

	// The default file of a program without a profile on it counts once
	// it exists.
	put(t, "codex-0.46.0-config.toml", filepath.Join(home, ".codex", "config.toml"))
	got = Targets(profiles, home)
	if !slices.ContainsFunc(got, func(x Target) bool { return x.ID == "codex" }) {
		t.Fatalf("the existing default codex file is not a target: %v", got)
	}
	if got := Targets(nil, t.TempDir()); len(got) != 0 {
		t.Fatalf("no profiles and no files gave %v", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// omoEntry writes a file of cli that holds only an omo entry with command
// and env (an http entry when command is "").
func omoEntry(t *testing.T, f *fakes, tg Target, command string, env map[string]string) {
	t.Helper()
	e := map[string]any{"command": command, "args": []string{"mcp", "stdio"}}
	if command == "" {
		e = map[string]any{"url": "https://omo.example.invalid/mcp", "headers": map[string]string{"Authorization": "Bearer user-oauth-secret-0a0a"}}
		if tg.CLI == api.CLIClaude {
			e["type"] = "http"
		}
	}
	if env != nil {
		e["env"] = env
	}
	if tg.CLI == api.CLICodex {
		var b strings.Builder
		b.WriteString("[mcp_servers.omo]\n")
		if command == "" {
			b.WriteString("url = \"https://omo.example.invalid/mcp\"\nhttp_headers = { Authorization = \"Bearer user-oauth-secret-0a0a\" }\n")
		} else {
			b.WriteString("command = \"" + command + "\"\nargs = [\"mcp\", \"stdio\"]\n")
			for k, v := range env {
				b.WriteString("env = { " + k + " = \"" + v + "\" }\n")
			}
		}
		writeFile(t, tg.ConfigPath, b.String())
		return
	}
	data, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"omo": e}})
	writeFile(t, tg.ConfigPath, string(data))
}

func TestInspectStates(t *testing.T) {
	f := newFakes(t)
	for _, cli := range programs {
		t.Run(cli, func(t *testing.T) {
			tg := target(t, f, cli, "")
			if st := f.clients.Inspect(tg); st.State != api.MCPClientAbsent || st.Error != "" {
				t.Fatalf("no file: %+v", st)
			}
			omoEntry(t, f, tg, f.opts.OmoPath, nil)
			if st := f.clients.Inspect(tg); st.State != api.MCPClientInstalled {
				t.Fatalf("the bridge: %s", st.State)
			}
			passHome := f.with(func(o *Options) { o.PassHome = true })
			if st := passHome.Inspect(tg); st.State != api.MCPClientOutdated {
				t.Fatalf("the bridge without OMO_HOME for another home: %s", st.State)
			}
			omoEntry(t, f, tg, f.opts.OmoPath, map[string]string{"OMO_HOME": f.omoHome})
			if st := passHome.Inspect(tg); st.State != api.MCPClientInstalled {
				t.Fatalf("the bridge with OMO_HOME: %s", st.State)
			}
			omoEntry(t, f, tg, "/usr/local/bin/omo", nil)
			if st := f.clients.Inspect(tg); st.State != api.MCPClientOutdated {
				t.Fatalf("another path: %s", st.State)
			}
			omoEntry(t, f, tg, "", nil)
			st := f.clients.Inspect(tg)
			if st.State != api.MCPClientForeign {
				t.Fatalf("an http entry named omo: %s", st.State)
			}
			if masked, _ := json.Marshal(MaskState(st)); bytes.Contains(masked, []byte("user-oauth-secret-0a0a")) {
				t.Fatalf("the masked state shows the entry's header: %s", masked)
			}
			writeFile(t, tg.ConfigPath, "{ this is [ not a config")
			st = f.clients.Inspect(tg)
			if st.State != api.MCPClientAbsent || !strings.Contains(st.Error, "cannot be read") || strings.Contains(st.Error, "this is") {
				t.Fatalf("a broken file: %+v", st)
			}
		})
	}
}

// TestPlansGolden pins every plan per program: install on an absent,
// installed, outdated and foreign entry, remove on an installed and an
// absent one.
func TestPlansGolden(t *testing.T) {
	f := newFakes(t)
	c := f.with(func(o *Options) { o.PassHome = true })
	for _, cli := range programs {
		t.Run(cli, func(t *testing.T) {
			tg := target(t, f, cli, "")
			plans := map[string]api.MCPClientPlan{}
			plan := func(label, action string) {
				p, err := c.Plan(tg, action)
				if err != nil {
					t.Fatal(err)
				}
				plans[label] = p
			}
			os.Remove(tg.ConfigPath)
			plan("install-absent", api.MCPClientInstall)
			plan("remove-absent", api.MCPClientRemove)
			omoEntry(t, f, tg, f.opts.OmoPath, map[string]string{"OMO_HOME": f.omoHome})
			plan("install-installed", api.MCPClientInstall)
			plan("remove-installed", api.MCPClientRemove)
			omoEntry(t, f, tg, "/usr/local/bin/omo", nil)
			plan("install-outdated", api.MCPClientInstall)
			omoEntry(t, f, tg, "", nil)
			plan("install-foreign", api.MCPClientInstall)
			golden(t, f, "plans-"+cli, plans)
			if data, _ := json.Marshal(plans); bytes.Contains(data, []byte("user-oauth-secret-0a0a")) {
				t.Fatal("a plan shows the value of a header")
			}
		})
	}
	acc := filepath.Join(f.home, "accounts", "work")
	p, err := c.Plan(target(t, f, api.CLIClaude, acc), api.MCPClientInstall)
	if err != nil {
		t.Fatal(err)
	}
	p.Target.ID = "claude-<hash of the account>"
	golden(t, f, "plan-claude-account", p)
	if _, err := c.Plan(target(t, f, api.CLIClaude, ""), "upgrade"); err == nil {
		t.Fatal("an unknown action was planned")
	}
}

// backupsOf lists the files below the backup folder.
func backupsOf(t *testing.T, f *fakes) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(f.opts.Backups, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	return out
}

func TestApplyInstallIsIdempotentAndRemoveUndoesIt(t *testing.T) {
	fixtures := map[string]string{api.CLIClaude: "claude-2.1.326.json", api.CLICodex: "codex-0.162.1-config.toml", api.CLIAgy: "agy-1.5.9-mcp_config.json"}
	for _, cli := range programs {
		t.Run(cli, func(t *testing.T) {
			f := newFakes(t)
			c := f.with(func(o *Options) { o.PassHome = true })
			tg := target(t, f, cli, "")
			put(t, fixtures[cli], tg.ConfigPath)
			original, _ := os.ReadFile(tg.ConfigPath)
			before := c.Inspect(tg)

			p, err := c.Plan(tg, api.MCPClientInstall)
			if err != nil {
				t.Fatal(err)
			}
			applied, err := c.Apply(context.Background(), tg, api.MCPClientInstall, p.Hash)
			if err != nil {
				t.Fatal(err)
			}
			if applied.State.State != api.MCPClientInstalled {
				t.Fatalf("after the install: %s", applied.State.State)
			}
			// The program kept every other entry.
			if len(applied.State.Entries) != len(before.Entries) {
				t.Fatalf("entries %d after the install, %d before", len(applied.State.Entries), len(before.Entries))
			}
			// The backup is the file as it was, private, in omo's home.
			if applied.Backup == "" || !strings.HasPrefix(applied.Backup, f.opts.Backups) || !strings.Contains(applied.Backup, "-mcp-"+cli) {
				t.Fatalf("backup %q", applied.Backup)
			}
			saved, _ := os.ReadFile(applied.Backup)
			if !bytes.Equal(saved, original) {
				t.Fatal("the backup differs from the file before")
			}
			if info, _ := os.Stat(applied.Backup); info.Mode().Perm() != 0o600 {
				t.Fatalf("backup mode %v", info.Mode().Perm())
			}
			calls := f.calls(t)
			if len(calls) == 0 || calls[len(calls)-1].Args[1] == "remove" {
				t.Fatalf("calls %+v", calls)
			}
			if cli == api.CLIClaude && !slices.Contains(calls[len(calls)-1].Args, "user") {
				t.Fatalf("claude was not told the user scope: %v", calls[len(calls)-1].Args)
			}
			if !slices.ContainsFunc(calls[len(calls)-1].Args, func(a string) bool { return strings.Contains(a, "OMO_HOME") }) {
				t.Fatalf("OMO_HOME of another home was not passed: %v", calls[len(calls)-1].Args)
			}

			// Again: nothing to do, nothing runs, no backup.
			n, backups := len(calls), len(backupsOf(t, f))
			p, err = c.Plan(tg, api.MCPClientInstall)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Commands) != 0 || !strings.Contains(p.Message, "already installed") {
				t.Fatalf("second plan %+v", p)
			}
			if _, err := c.Apply(context.Background(), tg, api.MCPClientInstall, p.Hash); err != nil {
				t.Fatal(err)
			}
			if len(f.calls(t)) != n || len(backupsOf(t, f)) != backups {
				t.Fatal("an install of an installed entry ran a command or made a backup")
			}

			// Remove is the undo.
			p, err = c.Plan(tg, api.MCPClientRemove)
			if err != nil {
				t.Fatal(err)
			}
			applied, err = c.Apply(context.Background(), tg, api.MCPClientRemove, p.Hash)
			if err != nil {
				t.Fatal(err)
			}
			if applied.State.State != api.MCPClientAbsent || applied.Backup == "" {
				t.Fatalf("after the removal: %+v", applied)
			}
			p, _ = c.Plan(tg, api.MCPClientRemove)
			if len(p.Commands) != 0 {
				t.Fatalf("remove on an absent entry plans %v", p.Commands)
			}
		})
	}
}

func TestApplySetsTheAccountFolderOnlyForAccountTargets(t *testing.T) {
	f := newFakes(t)
	for _, cli := range []string{api.CLIClaude, api.CLICodex} {
		acc := filepath.Join(f.home, "accounts", cli)
		for _, tg := range []Target{target(t, f, cli, ""), target(t, f, cli, acc)} {
			p, err := f.clients.Plan(tg, api.MCPClientInstall)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.clients.Apply(context.Background(), tg, api.MCPClientInstall, p.Hash); err != nil {
				t.Fatal(err)
			}
			calls := f.calls(t)
			env := calls[len(calls)-1].Env
			v, set := env[accountVariable(cli)]
			if tg.Account == "" && set {
				t.Fatalf("%s: the default target ran with %s=%s", tg.ID, accountVariable(cli), v)
			}
			if tg.Account != "" && v != acc {
				t.Fatalf("%s: %s=%q, want %q", tg.ID, accountVariable(cli), v, acc)
			}
			if st := f.clients.Inspect(tg); st.State != api.MCPClientInstalled {
				t.Fatalf("%s: %s", tg.ID, st.State)
			}
		}
	}
}

func TestApplyRefusesAChangedFile(t *testing.T) {
	f := newFakes(t)
	tg := target(t, f, api.CLIAgy, "")
	p, err := f.clients.Plan(tg, api.MCPClientInstall)
	if err != nil {
		t.Fatal(err)
	}
	omoEntry(t, f, tg, "/elsewhere/omo", nil)
	_, err = f.clients.Apply(context.Background(), tg, api.MCPClientInstall, p.Hash)
	var e *api.Error
	if !errors.As(err, &e) || e.Code != api.CodeConflict || !strings.Contains(e.Message, "review again") {
		t.Fatalf("apply of a stale plan: %v", err)
	}
	if len(f.calls(t)) != 0 || len(backupsOf(t, f)) != 0 {
		t.Fatal("a stale plan ran a command or made a backup")
	}
}

// TestApplyFailedStepLeavesAClearState: the second command of a
// replacement fails; the answer names the step, the state the entry is in
// now and the backup, and never a value of the file.
func TestApplyFailedStepLeavesAClearState(t *testing.T) {
	f := newFakes(t)
	tg := target(t, f, api.CLIClaude, "")
	put(t, "claude-2.1.326.json", tg.ConfigPath)
	original, _ := os.ReadFile(tg.ConfigPath)
	f.fail(t, "add-json", "error: cannot write config near Bearer claude-tracker-secret-7f3a")
	p, err := f.clients.Plan(tg, api.MCPClientInstall)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Commands) != 2 {
		t.Fatalf("an outdated entry plans %v", p.Commands)
	}
	applied, err := f.clients.Apply(context.Background(), tg, api.MCPClientInstall, p.Hash)
	var e *api.Error
	if !errors.As(err, &e) {
		t.Fatalf("apply: %v", err)
	}
	for _, want := range []string{"step 2 of 2", "now absent", applied.Backup} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("message %q lacks %q", e.Message, want)
		}
	}
	if strings.Contains(e.Message, "claude-tracker-secret-7f3a") {
		t.Fatalf("the message shows a value: %s", e.Message)
	}
	details, _ := json.Marshal(e.Details)
	if bytes.Contains(details, []byte("claude-search-secret-c0de")) || !bytes.Contains(details, []byte(`"state":"absent"`)) {
		t.Fatalf("details %s", details)
	}
	saved, _ := os.ReadFile(applied.Backup)
	if !bytes.Equal(saved, original) {
		t.Fatal("the backup is not the file before")
	}
}

func TestApplyWithoutBackupFolderChangesNothing(t *testing.T) {
	f := newFakes(t)
	blocker := filepath.Join(f.omoHome, "blocker")
	writeFile(t, blocker, "x")
	c := f.with(func(o *Options) { o.Backups = filepath.Join(blocker, "backups") })
	tg := target(t, f, api.CLIAgy, "")
	put(t, "agy-1.5.9-mcp_config.json", tg.ConfigPath)
	p, _ := c.Plan(tg, api.MCPClientInstall)
	if _, err := c.Apply(context.Background(), tg, api.MCPClientInstall, p.Hash); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("apply without a backup: %v", err)
	}
	if len(f.calls(t)) != 0 {
		t.Fatal("a command ran without a backup")
	}
}

func TestPlanHash(t *testing.T) {
	f := newFakes(t)
	tg := target(t, f, api.CLICodex, "")
	a, _ := f.clients.Plan(tg, api.MCPClientInstall)
	b, _ := f.clients.Plan(tg, api.MCPClientInstall)
	if a.Hash == "" || a.Hash != b.Hash {
		t.Fatalf("hashes %q and %q of the same plan", a.Hash, b.Hash)
	}
	c, _ := f.with(func(o *Options) { o.PassHome = true }).Plan(tg, api.MCPClientInstall)
	r, _ := f.clients.Plan(tg, api.MCPClientRemove)
	if c.Hash == a.Hash || r.Hash == a.Hash {
		t.Fatal("different plans have the same hash")
	}
}

func TestMaskValue(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"info":                   "***",
		"Bearer abc123":          "Bearer ***",
		"Bearer ${TOKEN}":        "Bearer ${TOKEN}",
		"${A}:${B:-x}":           "${A}***${B:-x}",
		"key-${ID}-suffix":       "***${ID}***",
		"Bearer":                 "***",
		"https://u:p@h/x?k=v":    "***",
		"token with  two spaces": "token ***",
	} {
		if got := maskValue(in); got != want {
			t.Errorf("maskValue(%q) = %q, want %q", in, got, want)
		}
	}
	if got := maskURL("https://user:pw-secret@h.example.invalid/mcp?key=q-secret&x=1"); strings.Contains(got, "secret") || !strings.Contains(got, "h.example.invalid/mcp") {
		t.Errorf("maskURL: %s", got)
	}
}
