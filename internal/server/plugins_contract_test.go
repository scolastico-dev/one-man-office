package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestPluginActionNavigationContract(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	p, _ := s.settings.Plugin("rec")
	p.Config["greeting"] = "hidden-secret"
	if err := s.settings.PutPluginIf(p.Revision, p); err != nil {
		t.Fatal(err)
	}
	s.reloadPlugins(true)
	run := func(source string) *http.Response {
		t.Helper()
		if err := os.WriteFile(filepath.Join(s.opts.Layout.Plugins, "rec/ping.lua"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		s.reloadPlugins(true)
		return asUser(b, s, "POST", "/api/plugins/rec/actions/ping", map[string]any{})
	}
	for _, tc := range []struct {
		source string
		want   api.PluginRunResult
	}{
		{`return {url="https://example.test/a%2Fb?folder=%2Ftmp#L2",message="ready"}`, api.PluginRunResult{Message: "ready", URL: "https://example.test/a%2Fb?folder=%2Ftmp#L2"}},
		{`return {forward_id="0123456789abcdef0123456789abcdef"}`, api.PluginRunResult{ForwardID: "0123456789abcdef0123456789abcdef"}},
	} {
		resp := run(tc.source)
		wantStatus(t, resp.StatusCode, http.StatusOK)
		if got := body[api.PluginRunResult](t, resp); got != tc.want {
			t.Fatalf("got %+v want %+v", got, tc.want)
		}
	}
	resp := run(`return {url="https://user:password@example.test/"}`)
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	resp = run(`return {url="https://example.test/hidden-secret"}`) // configured greeting is secret; refuse instead of mutating URL.
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	resp = run(`return {message=config.greeting .. "\226\128\174" .. string.rep("x",2000),url="https://example.test/"}`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got := body[api.PluginRunResult](t, resp)
	if strings.Contains(got.Message, "hidden-secret") || !strings.Contains(got.Message, `\u{202E}`) || len([]rune(got.Message)) > 1000 || got.URL != "https://example.test/" {
		t.Fatalf("answer cleaning: %+v", got)
	}
}

func TestPluginActionProjectContext(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	maker := newTicketMaker(t, s)
	n := maker.create()
	other, err := s.settings.AddProject(newRepo(t), "other")
	if err != nil {
		t.Fatal(err)
	}
	// Project context carries an id; folder lookup is a separate runtime feature.
	source := `assert(event.data.project.id ~= nil); return {message=event.data.project.id}`
	if err := os.WriteFile(filepath.Join(s.opts.Layout.Plugins, "rec/ping.lua"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.opts.Layout.Plugins, "rec/hello.lua"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	s.reloadPlugins(true)
	for _, tc := range []struct {
		action  string
		req     map[string]any
		status  int
		message string
	}{
		{"ping", map[string]any{"project": maker.project}, http.StatusOK, maker.project},
		{"hello", map[string]any{"ticket": n, "project": maker.project}, http.StatusOK, maker.project},
		{"hello", map[string]any{"ticket": n, "project": other.ID}, http.StatusUnprocessableEntity, ""},
		{"ping", map[string]any{"project": "missing"}, http.StatusNotFound, ""},
		{"ping", map[string]any{"project": "../project"}, http.StatusNotFound, ""},
		{"ping", map[string]any{"project": maker.project, "typo": true}, http.StatusBadRequest, ""},
	} {
		resp := asUser(b, s, "POST", "/api/plugins/rec/actions/"+tc.action, tc.req)
		wantStatus(t, resp.StatusCode, tc.status)
		if tc.status == http.StatusOK {
			if got := body[api.PluginRunResult](t, resp); got.Message != tc.message {
				t.Fatalf("got %+v", got)
			}
		} else {
			resp.Body.Close()
		}
	}
}

func TestPluginActionRejectsBoardLoginToken(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	for _, address := range []string{"https://example.test/#" + s.token, "https://example.test/" + s.token, "https://example.test/?folder=" + s.token, "https://example.test/?folder=%" + fmt.Sprintf("%02x", s.token[0]) + s.token[1:]} {
		source := `return {url=` + strconv.Quote(address) + `}`
		if err := os.WriteFile(filepath.Join(s.opts.Layout.Plugins, "rec/ping.lua"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		s.reloadPlugins(true)
		resp := asUser(b, s, "POST", "/api/plugins/rec/actions/ping", map[string]any{})
		wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), s.token) {
			t.Fatal("token leaked in error")
		}
	}
}
