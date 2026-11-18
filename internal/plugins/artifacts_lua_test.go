package plugins

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestArtifactsLuaStrictBoundScopeAndRuntime(t *testing.T) {
	e := newEnv(t)
	source := `local host=omo.runtime_info()
assert(host.os ~= "" and host.arch ~= "")
local own,err=omo.artifact_status("tool")
assert(own == nil and err ~= "")
local invalid={
 {key="tool",url="https://example.test",format="tar.gz",plugin="q"},
 {key="tool",url="https://example.test",format="tar.gz",destination="/tmp/elsewhere"},
 {key="tool",url="https://example.test",format="tar.gz",strip_components=1.5},
 {key="tool",url="https://example.test",format="tar.gz",retry="yes"},
}
for _,options in ipairs(invalid) do local s,err=omo.artifact_start(options);assert(s==nil and err~="") end
return "bound helpers checked"`
	e.plugin("p", manifestOf(map[string]any{"event": EventManual, "name": "check", "description": "Check", "lua": "check.lua"}), map[string]string{"check.lua": source})
	if errs := e.load(enabled("p", nil)); len(errs) != 0 {
		t.Fatal(errs)
	}
	answer, err := e.m.Trigger(t.Context(), "p", "check", nil)
	if err != nil || answer != "bound helpers checked" {
		t.Fatalf("Lua bridge: %s %v", answer, err)
	}
}

func TestArtifactsLuaInstallAndTaskLaunch(t *testing.T) {
	// Archive a tiny real Go fixture executable; no system shell/Node/tar needed.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	executable := "fixture"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	raw := tarFixture(t, archiveMember{name: "pkg/" + executable, body: string(binary), mode: 0o755})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer server.Close()
	e := newEnv(t)
	e.m.o.ArtifactHTTP = server.Client()
	req := artifactRequest(server.URL, raw)
	opts, _ := json.Marshal(req)
	source := fmt.Sprintf(`local req=assert(omo.json_decode(%q))
if not omo.store_get("install") then
 local s,err=omo.artifact_start(req);assert(s,err);assert(not s.path)
 omo.store_set("install",true);return s.state
end
local s,err=omo.artifact_status("tool");assert(s,err)
if s.state ~= "ready" then return s.state end
local task,err=omo.task_start{name="installed",command=s.path..%q,args={"plugin-helper","sleep"}}
assert(task,err);return task.id`, string(opts), "/"+executable)
	e.plugin("p", manifestOf(map[string]any{"event": EventManual, "name": "launch", "description": "Launch", "lua": "launch.lua"}), map[string]string{"launch.lua": source})
	if errs := e.load(enabled("p", nil)); len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, err = e.m.Trigger(t.Context(), "p", "launch", nil); err != nil {
		t.Fatal(err)
	}
	if s := waitArtifact(t, e.m, "p", "tool"); s.State != "ready" {
		t.Fatal(s)
	}
	if _, err = e.m.Trigger(t.Context(), "p", "launch", nil); err != nil {
		t.Fatal(err)
	}
	tasks, err := e.m.Tasks("p")
	if err != nil || len(tasks) != 1 || tasks[0].State != "running" {
		t.Fatal(tasks, err)
	}
	if errs := e.load(api.PluginSettings{Name: "p", Enabled: false}); len(errs) != 0 {
		t.Fatal(errs)
	}
	tasks, _ = e.m.Tasks("p")
	if tasks[0].State != "exited" || tasks[0].Reason != TaskPluginDisabled {
		t.Fatal("installed executable survived disable", tasks)
	}
	data := filepath.Join(e.root, ".data", "p")
	if _, err = os.Stat(data); err != nil {
		t.Fatal("disable deleted cache", err)
	}
}
