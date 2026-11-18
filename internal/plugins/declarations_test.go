package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const declarationManifest = `{
 "version":"1.0.0",
 "default_config":{"editor":"code","licensed":false,"minutes":30,"name":"Editor","extra":{"keep":true}},
 "hooks":[{"event":"manual","name":"open","description":"Open","scope":"ticket","lua":"a.lua"},{"event":"manual","name":"repo","description":"Repo","lua":"a.lua"},{"event":"omo_started","lua":"a.lua"}],
 "buttons":[{"label":"Open","place":"ticket","action":"open","per_worktree":true},{"label":"Repo","place":"project","action":"repo"},{"label":"All","place":"sidebar","action":"repo"}],
 "frontend":{"script":"ui/main.js","assets":"ui/assets"},
 "config_fields":[{"key":"editor","label":"Editor","type":"select","choices":[{"value":"code","label":"Code"},{"value":"open","label":"Open"}]},{"key":"licensed","label":"Accept terms","type":"boolean"},{"key":"minutes","label":"Minutes","type":"number"},{"key":"name","label":"Name","type":"string"}]
}`

func TestExtendedManifest(t *testing.T) {
	m, err := ParseManifest([]byte(declarationManifest))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if len(got["buttons"].([]any)) != 3 || len(got["config_fields"].([]any)) != 4 || got["frontend"].(map[string]any)["script"] != "ui/main.js" {
		t.Fatalf("declarations missing: %s", raw)
	}
	if _, ok := m.DefaultConfig["extra"]; !ok {
		t.Fatal("unlisted config lost")
	}
}

func TestExtendedManifestRefusals(t *testing.T) {
	for _, tc := range []struct{ old, new, want string }{
		{`"place":"ticket"`, `"place":"other"`, "place"},
		{`"action":"open"`, `"action":"missing"`, "action"},
		{`"action":"open"`, `"action":"repo"`, "scope"},
		{`"place":"project"`, `"place":"ticket"`, "scope"},
		{`"per_worktree":true`, `"per_worktree":"yes"`, "type"},
		{`"place":"project"`, `"place":"project","per_worktree":true`, "per_worktree"},
		{`"label":"Open"`, `"label":""`, "label"},
		{`"place":"ticket"`, `"place":"ticket","typo":true`, "unknown"},
		{`"assets":"ui/assets"`, `"assets":"ui/assets","typo":true`, "unknown"},
		{`"script":"ui/main.js"`, `"script":5`, "type"},
		{`"label":"Editor","type"`, `"label":"Editor","typo":true,"type"`, "unknown"},
		{`"value":"code","label":"Code"`, `"value":"code","label":"Code","typo":true`, "unknown"},
		{`"value":"open"`, `"value":"code"`, "duplicate"},
		{`"key":"licensed"`, `"key":"editor"`, "duplicate"},
		{`"type":"select"`, `"type":"date"`, "type"},
		{`"type":"select","choices":[{"value":"code","label":"Code"},{"value":"open","label":"Open"}]`, `"type":"select","choices":[]`, "choices"},
		{`"value":"code"`, `"value":""`, "value"},
		{`"licensed":false`, `"licensed":"yes"`, "default_config"},
		{`"minutes":30`, `"minutes":"30"`, "default_config"},
		{`"editor":"code"`, `"editor":"missing"`, "default_config"},
		{`"name":"Editor"`, `"name":false`, "default_config"},
		{`"key":"minutes"`, `"key":"../minutes"`, "key"},
		{`"type":"boolean"`, `"type":"boolean","choices":[]`, "choices"},
		{`"buttons":[`, `"buttons":[null,`, "object"},
		{`"config_fields":[`, `"config_fields":[null,`, "object"},
		{`"choices":[`, `"choices":[null,`, "object"},
		{`"frontend":{"script":"ui/main.js","assets":"ui/assets"}`, `"frontend":null`, "object"},
		{`"buttons":[{"label":"Open","place":"ticket","action":"open","per_worktree":true},{"label":"Repo","place":"project","action":"repo"},{"label":"All","place":"sidebar","action":"repo"}]`, `"buttons":null`, "list"},
		{`"script":"ui/main.js"`, `"script":null`, "type"},
	} {
		t.Run(tc.want+tc.new, func(t *testing.T) {
			_, err := ParseManifest([]byte(strings.Replace(declarationManifest, tc.old, tc.new, 1)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
	// A duplicated declaration is ambiguous even if its label differs.
	raw := strings.Replace(declarationManifest, `"buttons":[`, `"buttons":[{"label":"Again","place":"ticket","action":"open"},`, 1)
	if _, err := ParseManifest([]byte(raw)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate button: %v", err)
	}
}

func TestFrontendPortablePaths(t *testing.T) {
	for _, p := range []string{"../x.js", "/x.js", `C:\x.js`, `C:x.js`, `\\host\share\x.js`, "ui/../x.js", "./x.js", "ui//x.js", "ui/.git/x.js", ".data/x.js", "settings/x.js", "ui/settings.yaml", "ui/.omo-install.json", "ui/CON", "ui/COM¹.txt", "ui/LPT²", "ui/x.js.", "ui/a:b", "ui/x\u202e.js"} {
		t.Run(p, func(t *testing.T) {
			raw := strings.Replace(declarationManifest, `"ui/main.js"`, string(mustJSON(t, p)), 1)
			if _, err := ParseManifest([]byte(raw)); err == nil {
				t.Fatalf("accepted %q", p)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func frontendTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ui/assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for p, s := range map[string]string{"plugin.json": declarationManifest, "a.lua": `return "hello"`, "ui/main.js": "export {}", "ui/assets/pic.txt": "picture"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestFrontendExistsAndConfined(t *testing.T) {
	for _, tc := range []string{"missing script", "missing assets", "script directory", "assets file", "script symlink", "ancestor symlink", "asset symlink", "private asset"} {
		t.Run(tc, func(t *testing.T) {
			dir := frontendTree(t)
			script := filepath.Join(dir, "ui/main.js")
			assets := filepath.Join(dir, "ui/assets")
			switch tc {
			case "missing script":
				_ = os.Remove(script)
			case "missing assets":
				_ = os.RemoveAll(assets)
			case "script directory":
				_ = os.Remove(script)
				_ = os.Mkdir(script, 0700)
			case "assets file":
				_ = os.RemoveAll(assets)
				_ = os.WriteFile(assets, []byte("x"), 0600)
			case "script symlink":
				_ = os.Remove(script)
				if err := os.Symlink(filepath.Join(dir, "a.lua"), script); err != nil {
					t.Skip(err)
				}
			case "ancestor symlink":
				_ = os.Rename(filepath.Join(dir, "ui"), filepath.Join(dir, "real"))
				if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "ui")); err != nil {
					t.Skip(err)
				}
			case "asset symlink":
				if err := os.Symlink(filepath.Join(dir, "a.lua"), filepath.Join(assets, "linked")); err != nil {
					t.Skip(err)
				}
			case "private asset":
				_ = os.WriteFile(filepath.Join(assets, "settings.yaml"), []byte("secret"), 0600)
			}
			if _, err := ReadManifest(dir); err == nil {
				t.Fatalf("accepted %s", tc)
			}
		})
	}
	if _, err := ReadManifest(frontendTree(t)); err != nil {
		t.Fatal(err)
	}
}

func TestFrontendContentIdentity(t *testing.T) {
	dir := frontendTree(t)
	load := func(root string) map[string]any {
		t.Helper()
		e := newEnv(t)
		raw, err := os.ReadFile(filepath.Join(root, "plugin.json"))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || p == filepath.Join(root, "plugin.json") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			b, err := os.ReadFile(p)
			files[rel] = string(b)
			return err
		})
		if err := os.MkdirAll(filepath.Join(e.root, "p/ui/assets"), 0700); err != nil {
			t.Fatal(err)
		}
		e.plugin("p", string(raw), files)
		if errs := e.load(enabled("p", nil)); len(errs) > 0 {
			t.Fatal(errs)
		}
		b, _ := json.Marshal(e.info("p"))
		var got map[string]any
		_ = json.Unmarshal(b, &got)
		return got
	}
	first := load(dir)
	id, ok := first["content_id"].(string)
	if !ok || len(id) != 64 {
		t.Fatalf("identity: %v", first)
	}
	if other := load(dir); other["content_id"] != id {
		t.Fatalf("identity depends on installation folder: %v", other)
	}
	if len(first["buttons"].([]any)) != 3 || len(first["config_fields"].([]any)) != 4 {
		t.Fatal(first)
	}
	_ = os.WriteFile(filepath.Join(dir, "ui/main.js"), []byte("export const a = 1"), 0600)
	second := load(dir)
	if second["content_id"] == id {
		t.Fatal("script edit did not change identity")
	}
	_ = os.WriteFile(filepath.Join(dir, "ui/assets/pic.txt"), []byte("edited"), 0600)
	if third := load(dir); third["content_id"] == second["content_id"] {
		t.Fatal("asset edit did not change identity")
	}
}

func TestEmptyPluginMetadata(t *testing.T) {
	e := newEnv(t)
	e.load(enabled("missing", nil))
	b, _ := json.Marshal(e.info("missing"))
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	for _, key := range []string{"buttons", "config_fields", "frontend"} {
		if got[key] == nil {
			t.Fatalf("%s must be an empty collection: %s", key, b)
		}
	}
}

func TestDeclarationRoundTrip(t *testing.T) {
	m, err := ParseManifest([]byte(declarationManifest))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseManifest(mustJSON(t, m)); err != nil {
		t.Fatal(err)
	}
}

func TestDeclarationResourceLimits(t *testing.T) {
	for _, tc := range []struct {
		field string
		entry any
		count int
	}{
		{"buttons", map[string]any{"label": "Open", "place": "ticket", "action": "open"}, 65},
		{"config_fields", map[string]any{"key": "name", "label": "Name", "type": "string"}, 65},
	} {
		var raw map[string]any
		_ = json.Unmarshal([]byte(declarationManifest), &raw)
		entries := []any{}
		for range tc.count {
			entries = append(entries, tc.entry)
		}
		raw[tc.field] = entries
		if _, err := ParseManifest(mustJSON(t, raw)); err == nil || !strings.Contains(err.Error(), "64") {
			t.Fatalf("%s: %v", tc.field, err)
		}
	}
	for _, tc := range []struct{ old, new string }{
		{`"label":"Open"`, `"label":"` + strings.Repeat("x", 201) + `"`},
		{`"key":"editor"`, `"key":"` + strings.Repeat("x", 65) + `"`},
		{`"value":"code"`, `"value":"` + strings.Repeat("x", 65) + `"`},
	} {
		if _, err := ParseManifest([]byte(strings.Replace(declarationManifest, tc.old, tc.new, 1))); err == nil {
			t.Fatal("accepted oversized field")
		}
	}
	if _, err := ParseManifest([]byte(`{"hooks":[],"description":"` + strings.Repeat("x", MaxManifestBytes) + `"}`)); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("manifest size: %v", err)
	}
	dir := frontendTree(t)
	if err := os.WriteFile(filepath.Join(dir, "ui/main.js"), []byte(strings.Repeat("x", MaxFrontendScriptBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(dir); err == nil {
		t.Fatal("oversize script accepted")
	}
}

func TestLegacyDeclarationGuidance(t *testing.T) {
	for _, name := range []string{"javascript", "files", "startup", "load"} {
		_, err := ParseManifest([]byte(`{"hooks":[],"` + name + `":true}`))
		want := "frontend"
		if name == "startup" || name == "load" {
			want = "omo_started"
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, event := range []string{"startup", "load", "company_load", "company_startup", "shutdown", "unload", "company_shutdown"} {
		_, err := ParseManifest([]byte(`{"hooks":[{"event":"` + event + `","lua":"a.lua"}]}`))
		want := "omo_started"
		if event == "company_load" {
			want = "frontend"
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", event, err)
		}
	}
}

func TestConfigNumberDefaultFinite(t *testing.T) {
	raw := strings.Replace(declarationManifest, `"minutes":30`, `"minutes":1e999`, 1)
	if _, err := ParseManifest([]byte(raw)); err == nil || !strings.Contains(err.Error(), "default_config") {
		t.Fatalf("unrepresentable numeric default: %v", err)
	}
}

func TestOverlappingFrontendBounds(t *testing.T) {
	for _, bound := range []string{"bytes", "entries"} {
		t.Run(bound, func(t *testing.T) {
			dir := t.TempDir()
			_ = os.Mkdir(filepath.Join(dir, "ui"), 0700)
			manifest := `{"hooks":[],"frontend":{"script":"ui/main.js","assets":"ui"}}`
			if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "ui/main.js"), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			if bound == "bytes" {
				file, err := os.Create(filepath.Join(dir, "ui/large.bin"))
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(MaxFrontendBytes - 1); err != nil {
					t.Fatal(err)
				}
				file.Close()
			} else {
				for i := range MaxFrontendEntries - 2 {
					if err := os.WriteFile(filepath.Join(dir, "ui", fmt.Sprintf("asset%d", i)), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := ReadManifest(dir); err != nil {
				t.Fatalf("valid unique frontend at %s limit rejected: %v", bound, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "ui/extra"), []byte("xx"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadManifest(dir); err == nil {
				t.Fatalf("frontend over %s limit accepted", bound)
			}
		})
	}
}
