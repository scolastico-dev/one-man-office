package mcpclients

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// The test binary is also the fake claude, codex and agy: a small sh script
// per program, first in the lookup of the tests, runs it with OMO_FAKECLI
// set. The fake records its arguments and the account variables into a log
// and edits the program's file in the temporary home as the real program's
// "mcp add" and "mcp remove" would. A file "fail" next to the scripts makes
// it fail: its first line is the subcommand to fail, its second line what
// it prints.
func TestMain(m *testing.M) {
	if cli := os.Getenv("OMO_FAKECLI"); cli != "" {
		os.Exit(fakeCLI(cli, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// call is one recorded run of a fake program.
type call struct {
	CLI  string            `json:"cli"`
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
}

func fakeCLI(cli string, args []string) int {
	dir := os.Getenv("OMO_FAKECLI_DIR")
	env := map[string]string{}
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "HOME"} {
		if v, ok := os.LookupEnv(k); ok {
			env[k] = v
		}
	}
	line, _ := json.Marshal(call{CLI: cli, Args: args, Env: env})
	f, err := os.OpenFile(filepath.Join(dir, "calls.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	f.Write(append(line, '\n'))
	f.Close()
	if len(args) < 3 || args[0] != "mcp" {
		fmt.Fprintln(os.Stderr, "usage: mcp add|add-json|remove")
		return 2
	}
	sub := args[1]
	if data, err := os.ReadFile(filepath.Join(dir, "fail")); err == nil {
		lines := strings.SplitN(string(data), "\n", 2)
		if strings.TrimSpace(lines[0]) == sub {
			if len(lines) > 1 {
				fmt.Fprintln(os.Stderr, strings.TrimSpace(lines[1]))
			}
			return 1
		}
	}
	home := os.Getenv("HOME")
	switch cli {
	case "claude":
		path := filepath.Join(home, ".claude.json")
		if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
			path = filepath.Join(d, ".claude.json")
		}
		return editJSON(path, func(servers map[string]any) int {
			switch sub {
			case "add-json": // mcp add-json -s user NAME JSON
				name, raw := args[4], args[5]
				if _, ok := servers[name]; ok {
					fmt.Fprintf(os.Stderr, "MCP server %s already exists in user config\n", name)
					return 1
				}
				var v any
				if err := json.Unmarshal([]byte(raw), &v); err != nil {
					fmt.Fprintln(os.Stderr, "invalid JSON")
					return 1
				}
				servers[name] = v
			case "remove": // mcp remove -s user NAME
				name := args[4]
				if _, ok := servers[name]; !ok {
					fmt.Fprintf(os.Stderr, "No user-scoped MCP server found with name: %s\n", name)
					return 1
				}
				delete(servers, name)
			default:
				return 2
			}
			return 0
		})
	case "agy":
		path := filepath.Join(home, ".gemini", "config", "mcp_config.json")
		return editJSON(path, func(servers map[string]any) int {
			switch sub {
			case "add": // mcp add [-e K=V]... NAME CMD ARGS...
				rest := args[2:]
				env := map[string]any{}
				for len(rest) > 1 && rest[0] == "-e" {
					k, v, _ := strings.Cut(rest[1], "=")
					env[k] = v
					rest = rest[2:]
				}
				e := map[string]any{"command": rest[1], "args": toAny(rest[2:])}
				if len(env) > 0 {
					e["env"] = env
				}
				servers[rest[0]] = e
			case "remove":
				name := args[2]
				if _, ok := servers[name]; !ok {
					fmt.Fprintf(os.Stderr, "server %s not found\n", name)
					return 1
				}
				delete(servers, name)
			default:
				return 2
			}
			return 0
		})
	case "codex":
		path := filepath.Join(home, ".codex", "config.toml")
		if d := os.Getenv("CODEX_HOME"); d != "" {
			path = filepath.Join(d, "config.toml")
		}
		top := map[string]any{}
		if data, err := os.ReadFile(path); err == nil {
			if err := toml.Unmarshal(data, &top); err != nil {
				fmt.Fprintln(os.Stderr, "config.toml is broken")
				return 1
			}
		}
		servers, _ := top["mcp_servers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		switch sub {
		case "add": // mcp add NAME [--env K=V]... -- CMD ARGS...
			name, rest := args[2], args[3:]
			env := map[string]any{}
			for len(rest) > 1 && rest[0] == "--env" {
				k, v, _ := strings.Cut(rest[1], "=")
				env[k] = v
				rest = rest[2:]
			}
			if len(rest) == 0 || rest[0] != "--" {
				return 2
			}
			e := map[string]any{"command": rest[1], "args": rest[2:]}
			if len(env) > 0 {
				e["env"] = env
			}
			servers[name] = e
		case "remove":
			name := args[2]
			if _, ok := servers[name]; !ok {
				fmt.Fprintf(os.Stderr, "No MCP server named '%s' found\n", name)
				return 1
			}
			delete(servers, name)
		default:
			return 2
		}
		top["mcp_servers"] = servers
		data, err := toml.Marshal(top)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		os.MkdirAll(filepath.Dir(path), 0o700)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return 1
		}
		return 0
	}
	return 2
}

func toAny(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

// editJSON loads the JSON file at path (a missing file is {}), lets edit
// change its mcpServers and writes it back with every other key kept.
func editJSON(path string, edit func(map[string]any) int) int {
	top := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &top); err != nil {
			fmt.Fprintln(os.Stderr, "the config file is broken")
			return 1
		}
	}
	servers, _ := top["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if code := edit(servers); code != 0 {
		return code
	}
	top["mcpServers"] = servers
	data, _ := json.MarshalIndent(top, "", "  ")
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return 1
	}
	return 0
}

// fakes is a temporary machine: a home, omo's home, the fake programs and
// their log.
type fakes struct {
	home, omoHome, bin string
	clients            *Clients
	opts               Options
}

// newFakes builds a temporary home with fake programs. It never touches the
// real home: HOME of the programs' commands is the temporary one.
func newFakes(t *testing.T) *fakes {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake programs are sh scripts")
	}
	root := t.TempDir()
	f := &fakes{home: filepath.Join(root, "home"), omoHome: filepath.Join(root, "omo-home"), bin: filepath.Join(root, "bin")}
	for _, d := range []string{f.home, f.omoHome, f.bin} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, cli := range programs {
		script := fmt.Sprintf("#!/bin/sh\nOMO_FAKECLI=%s OMO_FAKECLI_DIR='%s' exec '%s' \"$@\"\n", cli, f.bin, self)
		if err := os.WriteFile(filepath.Join(f.bin, cli), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.opts = Options{
		Home:    f.home,
		OmoPath: filepath.Join(root, "bin-omo", "omo"),
		OmoHome: f.omoHome,
		Env:     []string{"HOME=" + f.home, "PATH=/usr/bin:/bin", "CLAUDE_CONFIG_DIR=/must/not/be/used", "CODEX_HOME=/must/not/be/used"},
		LookPath: func(name string) (string, error) {
			p := filepath.Join(f.bin, filepath.Base(name))
			if _, err := os.Stat(p); err != nil {
				return "", err
			}
			return p, nil
		},
		Getenv:  func(name string) (string, bool) { return "set", name == "WIKI_API_KEY" },
		Backups: filepath.Join(f.omoHome, "backups"),
	}
	f.clients = New(f.opts)
	return f
}

// with returns the fakes' clients built with changed options.
func (f *fakes) with(change func(*Options)) *Clients {
	o := f.opts
	change(&o)
	return New(o)
}

// calls returns the recorded runs of the fake programs.
func (f *fakes) calls(t *testing.T) []call {
	t.Helper()
	file, err := os.Open(filepath.Join(f.bin, "calls.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var out []call
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		var c call
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// fail makes the fake programs fail at sub, printing message.
func (f *fakes) fail(t *testing.T, sub, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.bin, "fail"), []byte(sub+"\n"+message+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// put copies a fixture from testdata to path.
func put(t *testing.T, fixture, path string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
