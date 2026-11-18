package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// hostileTitles are titles an agent could write: Lua source, shell syntax,
// format directives, hidden and control characters, JSON breakers.
var hostileTitles = []string{
	`"]] os.execute("touch pwned") --[[`,
	`end; omo.store_set("pwned", true); local x = function() `,
	`$(touch pwned) ` + "`touch pwned`" + ` ; touch pwned | & > pwned`,
	`%s %d %n %x %q %%`,
	"\u202etxt.exe\u2066 \u200b\x1b[31mred\x1b[0m \r\nsecond line \x00 nul",
	`{"event":"manual","data":{}}`,
	`omo.store_set`,
}

func TestLuaHookSandbox(t *testing.T) {
	e := newEnv(t)
	e.plugin("p", manifestOf(luaHook(EventTicketMoved, "h.lua")), map[string]string{"h.lua": `
local missing = {}
for _, name in ipairs({"io", "load", "loadstring", "dofile", "loadfile", "require", "debug", "package", "module", "getfenv", "setfenv"}) do
  if _G[name] ~= nil then missing[#missing + 1] = name end
end
for _, name in ipairs({"execute", "getenv", "remove", "rename", "exit", "tmpname"}) do
  if os[name] ~= nil then missing[#missing + 1] = "os." .. name end
end
if string.dump ~= nil then missing[#missing + 1] = "string.dump" end
for _, name in ipairs({"exec", "http", "sleep", "store_get", "store_set", "store_delete", "store_keys", "data_dir", "agents", "send_to_agent", "json_decode", "json_encode", "log", "now"}) do
  if type(omo[name]) ~= "function" then missing[#missing + 1] = "no omo." .. name end
end
omo.store_set("present", missing)
omo.store_set("time", type(os.time()))
`})
	e.load(enabled("p", nil))
	e.m.Dispatch(Event{Name: EventTicketMoved, Data: map[string]any{}})
	eventually(t, "the hook", func() bool { return e.stored("p", "time") != "" })
	if got := e.stored("p", "present"); got != "[]" && got != "{}" {
		t.Fatalf("reachable in a hook: %s", got)
	}
}

func TestCommandHookArgumentsAreTheManifests(t *testing.T) {
	e := newEnv(t)
	out := filepath.Join(t.TempDir(), "record.json")
	command := helperArgs(t, "record", out, "--flag=$(id)", "two words")
	e.plugin("cmd", manifestOf(map[string]any{"event": EventTicketCreated, "command": command}), nil)
	e.load(enabled("cmd", nil))
	for i, title := range hostileTitles {
		_ = os.Remove(out)
		ev := ticketEvent(7, title)
		e.m.Dispatch(ev)
		e.waitFinished("cmd", i+1)
		var rec struct {
			Args  []string `json:"args"`
			Env   []string `json:"env"`
			Stdin string   `json:"stdin"`
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(rec.Args, command[1:]) {
			t.Fatalf("argv = %q, want %q", rec.Args, command[1:])
		}
		var got struct {
			Event string `json:"event"`
			Data  struct {
				Ticket map[string]any `json:"ticket"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(rec.Stdin), &got); err != nil {
			t.Fatalf("standard input is not the event JSON: %v", err)
		}
		if got.Event != EventTicketCreated || got.Data.Ticket["title"] != strings.ToValidUTF8(title, "\uFFFD") {
			t.Fatalf("standard input = %s", rec.Stdin)
		}
		for _, kv := range rec.Env {
			if strings.Contains(kv, "pwned") || strings.Contains(kv, "%n") {
				t.Fatalf("event text in the environment: %q", kv)
			}
		}
	}
	for _, dir := range []string{filepath.Join(e.root, "cmd"), e.root} {
		if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
			t.Fatal("a title ran as a command")
		}
	}
}

func TestCommandHookEnvironment(t *testing.T) {
	t.Setenv("OMO_AGENT_ID", "agent-session-id")
	t.Setenv("OMO_HOME", "/somewhere")
	t.Setenv("omo_lower", "x")
	t.Setenv("PLUGIN_TEST_KEEP", "kept")
	e := newEnv(t)
	out := filepath.Join(t.TempDir(), "record.json")
	e.plugin("cmd", manifestOf(map[string]any{"event": EventTicketMoved, "command": helperArgs(t, "record", out)}), nil)
	e.load(enabled("cmd", map[string]any{"user_key": "abc"}))
	e.m.Dispatch(Event{Name: EventTicketMoved, Data: map[string]any{"ticket": TicketPayload(api.TicketSummary{Number: 3, Title: "MOVED-TITLE"}, ""), "from": "ready", "to": "in_progress"}})
	e.waitFinished("cmd", 1)
	var rec struct {
		Env []string `json:"env"`
	}
	raw, _ := os.ReadFile(out)
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{}
	for _, kv := range rec.Env {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
		if strings.Contains(v, "MOVED-TITLE") {
			t.Fatalf("event text in %s", k)
		}
	}
	for _, k := range []string{"OMO_AGENT_ID", "OMO_HOME", "omo_lower"} {
		if _, ok := vars[k]; ok {
			t.Errorf("%s is set for a plugin", k)
		}
	}
	data := filepath.Join(e.root, ".data", "cmd")
	want := map[string]string{
		"OMO_SOCKET": "/run/omo-test.sock", "OMO_PLUGIN_NAME": "cmd", "OMO_PLUGIN_EVENT": EventTicketMoved,
		"OMO_PLUGIN_CONFIG": `{"user_key":"abc"}`, "OMO_PLUGIN_DATA": data, "PLUGIN_TEST_KEEP": "kept",
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}
	if info, err := os.Stat(data); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("data folder: %v %v", info, err)
	}
}

func TestHostilePayloadsStayData(t *testing.T) {
	e := newEnv(t)
	e.plugin("lua", manifestOf(luaHook(EventTicketCreated, "h.lua")), map[string]string{"h.lua": `
local title = event.data.ticket.title
assert(type(title) == "string")
omo.log(title)
omo.store_set("t" .. event.data.ticket.number, title)
`})
	e.load(enabled("lua", nil))
	titles := append(append([]string(nil), hostileTitles...), strings.Repeat("A", 1<<20), strings.Repeat("é", 3000))
	for i, title := range titles {
		e.m.Dispatch(ticketEvent(i+1, title))
	}
	e.waitFinished("lua", len(titles))
	for i, title := range titles {
		var got string
		if err := json.Unmarshal([]byte(e.stored("lua", "t"+itoa(i+1))), &got); err != nil {
			t.Fatal(err)
		}
		want := cutText(title, MaxTextBytes)
		if got != want {
			t.Fatalf("title %d reached Lua as %q, want %q", i+1, cutRunes(got, 80), cutRunes(want, 80))
		}
	}
	if e.stored("lua", "pwned") != "" {
		t.Fatal("a title ran as Lua")
	}
	log := e.logText("lua")
	for _, bad := range []string{"\u202e", "\x1b", "\u200b", "\r", "\x00"} {
		if strings.Contains(log, bad) {
			t.Fatalf("log holds the hidden character %q: %q", bad, log)
		}
	}
	if !strings.Contains(log, `\u{202E}txt.exe`) {
		t.Fatalf("hidden characters are not escaped in the log: %q", log)
	}

	// An event too deep for the sandbox fails that run only.
	deep := map[string]any{}
	cur := deep
	for i := 0; i < 100; i++ {
		next := map[string]any{}
		cur["x"] = next
		cur = next
	}
	e.m.Dispatch(Event{Name: EventTicketCreated, Data: deep})
	e.m.Dispatch(ticketEvent(999, "after"))
	e.waitFinished("lua", len(titles)+2)
	if !strings.Contains(e.logText("lua"), "the event cannot be handed to Lua") {
		t.Fatalf("log = %s", e.logText("lua"))
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// Configuration values are plain, but a plugin's tokens never reach its
// log, its error texts, its state or an action's answer.
func TestConfigValuesNeverInLogsOrErrors(t *testing.T) {
	const userKey = "canary-USER-KEY-4f9a2c"
	const nested = "canary-NESTED-TOKEN-77d1"
	config := map[string]any{"user_key": userKey, "auth": map[string]any{"tokens": []any{nested}}}
	e := newEnv(t)
	e.plugin("lua", manifestOf(luaHook(EventTicketMoved, "h.lua"), map[string]any{"event": "manual", "name": "send", "description": "Send", "lua": "m.lua"}), map[string]string{
		"h.lua": `omo.log("using " .. config.user_key); print(config.auth.tokens[1]); error("request with " .. config.user_key .. " and " .. config.auth.tokens[1] .. " failed")`,
		"m.lua": `return {message = "sent with " .. config.user_key}`,
	})
	e.plugin("cmd", manifestOf(map[string]any{"event": EventTicketMoved, "command": helperArgs(t, "config-to-stderr")}, map[string]any{"event": "manual", "name": "fail", "description": "Fail", "command": helperArgs(t, "config-to-stderr")}), nil)
	e.load(enabled("lua", config), enabled("cmd", config))
	e.m.Dispatch(Event{Name: EventTicketMoved, Data: map[string]any{}})
	e.waitFinished("lua", 1)
	e.waitFinished("cmd", 1)
	if e.info("lua").State != StateError || e.info("cmd").State != StateError {
		t.Fatal("the failing hooks did not record their errors")
	}
	answer, err := e.m.Trigger(t.Context(), "lua", "send", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, cmdErr := e.m.Trigger(t.Context(), "cmd", "fail", nil)
	if cmdErr == nil {
		t.Fatal("a failing action answered")
	}
	var seen []string
	for _, name := range []string{"lua", "cmd"} {
		i := e.info(name)
		seen = append(seen, e.logText(name), i.Message)
	}
	seen = append(seen, answer, cmdErr.Error())
	e.mu.Lock()
	for _, c := range e.changes {
		raw, _ := json.Marshal(c)
		seen = append(seen, string(raw))
	}
	e.mu.Unlock()
	all := strings.Join(seen, "\n")
	for _, canary := range []string{userKey, nested} {
		if strings.Contains(all, canary) {
			t.Fatalf("a configuration value left the run: %s", all)
		}
	}
	if !strings.Contains(e.logText("lua"), "using [redacted]") || answer != "sent with [redacted]" {
		t.Fatalf("redaction did not happen where expected: log %q, answer %q", e.logText("lua"), answer)
	}
	if !strings.Contains(e.logText("cmd"), "config was") {
		t.Fatalf("command log = %q", e.logText("cmd"))
	}
}

func TestLuaExecPassesArgumentsAsTheyAre(t *testing.T) {
	e := newEnv(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e.plugin("p", manifestOf(luaHook(EventTicketCreated, "h.lua")), map[string]string{"h.lua": `
local out, err = omo.exec(config.exe, "plugin-helper", "print", event.data.ticket.title)
omo.store_set("out", out); omo.store_set("err", err)
local _, err2 = omo.exec(config.exe, "plugin-helper", "fail", "x")
omo.store_set("err2", err2)
local _, err3 = omo.exec("/no/such/program")
omo.store_set("err3", err3)
`})
	e.load(enabled("p", map[string]any{"exe": exe}))
	e.m.Dispatch(ticketEvent(1, "$(touch pwned); `id` %s"))
	eventually(t, "the hook", func() bool { return e.stored("p", "err3") != "" })
	if got := e.stored("p", "out"); got != `"$(touch pwned); `+"`id`"+` %s"` {
		t.Fatalf("output = %s", got)
	}
	if e.stored("p", "err") != `""` || e.stored("p", "err2") != `"the program exited with status 3"` || e.stored("p", "err3") != `"the program could not start"` {
		t.Fatalf("errors = %s %s %s", e.stored("p", "err"), e.stored("p", "err2"), e.stored("p", "err3"))
	}
	if _, err := os.Stat(filepath.Join(e.root, "p", "pwned")); err == nil {
		t.Fatal("an argument ran as a command")
	}
}

// The package runs Lua only through luabox, starts no shell and reads files
// only through homefile.
func TestNoOwnLuaStateShellOrUnboundedRead(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"lua.NewState(", "lua.OpenIo", "lua.OpenPackage", "lua.OpenDebug", "lua.OpenOs",
		`"/bin/sh"`, `"cmd.exe"`, "COMSPEC", `"-c"`,
		"os.ReadFile(", "os.Open(", "os.OpenFile(", "ioutil.",
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range forbidden {
			if strings.Contains(string(raw), bad) {
				t.Errorf("%s uses %s", f, bad)
			}
		}
	}
}
