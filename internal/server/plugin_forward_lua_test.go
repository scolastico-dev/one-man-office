package server

import (
	"github.com/scolastico-dev/one-man-office/internal/api"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginForwardLuaContract(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	p, _ := s.settings.Plugin("rec")
	p.Config["greeting"] = "hidden-forward-secret"
	if err := s.settings.PutPluginIf(p.Revision, p); err != nil {
		t.Fatal(err)
	}
	source := `
 assert(event.host.os ~= "" and event.host.arch ~= "")
 local port,err=omo.free_loopback_port();assert(port,err);assert(port>0 and port<65536)
 local bad=omo.forward_add{name="bad",port=port,plugin="someone"};assert(bad==nil)
 local bad=omo.forward_add{name="bad",port=port,start_path="/../escape"};assert(bad==nil)
 local bad=omo.forward_add{name="bad",port=port,start_path="/?access_token=secret"};assert(bad==nil)
 local bad=omo.forward_add{name="bad",port=port,start_path="/?folder="..config.greeting};assert(bad==nil)
 local f,err=omo.forward_add{name="tool",port=port,start_path="/folder?q=1",strip_prefix=true};assert(f,err)
 assert(f.id and f.address and f.start_path=="/folder?q=1" and f.base_path=="/proxy/"..f.id.."/")
 assert(f.activity.active_websockets==0 and f.activity.no_client_since ~= "")
 local all,err=omo.forward_list();assert(all,err);assert(#all==1 and all[1].id==f.id)
 assert(omo.forward_remove(f.id));assert(#omo.forward_list()==0)
 return {message="forward helpers ready"}
 `
	if err := os.WriteFile(filepath.Join(s.opts.Layout.Plugins, "rec/ping.lua"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	s.reloadPlugins(true)
	resp := asUser(b, s, "POST", "/api/plugins/rec/actions/ping", api.PluginRun{})
	if resp.StatusCode != 200 {
		raw := body[map[string]any](t, resp)
		t.Fatalf("status %d: %+v", resp.StatusCode, raw)
	}
	got := body[api.PluginRunResult](t, resp)
	if !strings.Contains(got.Message, "ready") {
		t.Fatalf("%+v", got)
	}
}
