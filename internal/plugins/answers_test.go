package plugins

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestActionAnswers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input any
		want  string
	}{
		{"empty", nil, `{"message":""}`},
		{"lua text", "hello", `{"message":"hello"}`},
		{"command text", []byte("hello\n"), `{"message":"hello"}`},
		{"json text", []byte(`"hello"`), `{"message":"hello"}`},
		{"message", map[string]any{"message": "hello"}, `{"message":"hello"}`},
		{"json message", []byte(`{"message":"hello"}`), `{"message":"hello"}`},
		{"lua url", map[string]any{"message": "open", "url": "https://example.test/a%2Fb?folder=%2Ftmp#L2"}, `{"message":"open","url":"https://example.test/a%2Fb?folder=%2Ftmp#L2"}`},
		{"command url", []byte(`{"url":"https://example.test/a%2Fb?folder=%2Ftmp#L2"}`), `{"message":"","url":"https://example.test/a%2Fb?folder=%2Ftmp#L2"}`},
		{"lua forward", map[string]any{"forward_id": "0123456789abcdef0123456789abcdef"}, `{"message":"","forward_id":"0123456789abcdef0123456789abcdef"}`},
		{"command forward", []byte(`{"message":"ready","forward_id":"0123456789abcdef0123456789abcdef"}`), `{"message":"ready","forward_id":"0123456789abcdef0123456789abcdef"}`},
		{"invalid utf8", []byte{'x', 0xff}, `{"message":"x�"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := answer(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(out)
			if string(b) != tc.want {
				t.Fatalf("got %s, want %s", b, tc.want)
			}
		})
	}
}

func TestActionRejectsUnsafeAnswers(t *testing.T) {
	bad := []map[string]any{
		{"url": "javascript:alert(1)"}, {"url": "data:text/html,hello"}, {"url": "//host/x"}, {"url": "/proxy/abc/"}, {"url": "https:/host/x"},
		{"url": "https://user:pass@host/"}, {"url": `https://host\@other.test/`}, {"url": "https://host/\n"}, {"url": "https://host/\u202e"},
		{"url": "https://host/?token=secret"}, {"url": "https://host/#token=secret"}, {"url": "https://host/#omo-token.secret"}, {"url": "https://host/_omo/proxy/bootstrap#secret"},
		{"url": "https://host/?__omo_proxy_grant=secret"}, {"url": "https://host/?x=1;access_token=secret"},
		{"url": "https://host/" + strings.Repeat("x", 8192)},
		{"url": 45}, {"url": nil}, {"url": ""}, {"forward_id": "../x"}, {"forward_id": "ABCDEF0123456789abcdef0123456789ab"}, {"forward_id": false}, {"forward_id": ""},
		{"url": "https://host/", "forward_id": "0123456789abcdef0123456789abcdef"}, {"url": "https://host/", "typo": true},
	}
	for _, input := range bad {
		for _, v := range []any{input, mustJSON(t, input)} {
			if got, err := answer(v); err == nil {
				t.Errorf("accepted %#v as %+v", v, got)
			}
		}
	}
}

func TestCommandAndLuaNavigation(t *testing.T) {
	e := newEnv(t)
	hooks := []map[string]any{}
	for name, body := range map[string]map[string]any{
		"cmd-url":     {"command": helperArgs(t, "print", `{"url":"https://example.test/a%2Fb?folder=%2Ftmp#L2"}`)},
		"cmd-forward": {"command": helperArgs(t, "print", `{"forward_id":"0123456789abcdef0123456789abcdef"}`)},
		"lua-url":     {"lua": "url.lua"}, "lua-forward": {"lua": "forward.lua"},
		"cmd-secret": {"command": helperArgs(t, "print", `{"url":"https://example.test/hidden-secret"}`)},
		"lua-secret": {"lua": "secret.lua"},
	} {
		body["event"] = "manual"
		body["name"] = name
		body["description"] = name
		hooks = append(hooks, body)
	}
	e.plugin("p", manifestOf(hooks...), map[string]string{"url.lua": `return {url="https://example.test/a%2Fb?folder=%2Ftmp#L2"}`, "forward.lua": `return {forward_id="0123456789abcdef0123456789abcdef"}`, "secret.lua": `return {url="https://example.test/%68idden-secret"}`})
	if errs := e.load(enabled("p", map[string]any{"password": "hidden-secret"})); len(errs) > 0 {
		t.Fatal(errs)
	}
	for _, name := range []string{"cmd-url", "lua-url", "cmd-forward", "lua-forward", "cmd-secret", "lua-secret"} {
		out, err := e.m.TriggerAction(t.Context(), "p", name, nil, nil)
		if strings.HasSuffix(name, "secret") {
			if err == nil || strings.Contains(err.Error(), "hidden-secret") {
				t.Fatalf("%s: %+v %v", name, out, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, "url") && out.URL != "https://example.test/a%2Fb?folder=%2Ftmp#L2" {
			t.Fatal(out)
		}
		if strings.HasSuffix(name, "forward") && out.ForwardID != "0123456789abcdef0123456789abcdef" {
			t.Fatal(out)
		}
	}
}

func TestLuaActionRetainsHTTPSecretsForAnswers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	e := newEnv(t)
	call := `assert(omo.http{url=` + string(mustJSON(t, server.URL)) + `,headers={Authorization="Bearer request-secret"}}); `
	e.plugin("p", manifestOf(
		map[string]any{"event": "manual", "name": "message", "description": "Message", "lua": "message.lua"},
		map[string]any{"event": "manual", "name": "url", "description": "URL", "lua": "url.lua"},
	), map[string]string{"message.lua": call + `return {message="request-secret"}`, "url.lua": call + `return {url="https://example.test/request-secret"}`})
	if errs := e.load(enabled("p", nil)); len(errs) > 0 {
		t.Fatal(errs)
	}
	out, err := e.m.TriggerAction(t.Context(), "p", "message", nil, nil)
	if err != nil || out.Message != "[redacted]" {
		t.Fatalf("HTTP secret in answer: %+v %v", out, err)
	}
	if out, err := e.m.TriggerAction(t.Context(), "p", "url", nil, nil); err == nil {
		t.Fatalf("HTTP secret URL accepted: %+v", out)
	}
}

func TestLegacyMessageAndCommandJSONCompatibility(t *testing.T) {
	for _, input := range []any{map[string]any{"message": "hello", "details": map[string]any{"ok": true}}, []byte(`{"message":"hello","details":{"ok":true}}`)} {
		out, err := answer(input)
		if err != nil || out.Message != "hello" {
			t.Fatalf("legacy message changed: %+v %v", out, err)
		}
	}
	for _, raw := range []string{`{"anything":true}`, `{"message":5}`, `{"message":null}`, `{}`, `[1,2]`, `null`, `true`} {
		out, err := answer([]byte(raw))
		if err != nil || out.Message != raw {
			t.Fatalf("legacy command stdout changed: %+v %v", out, err)
		}
	}
	for _, input := range []any{map[string]any{"message": false}, map[string]any{"message": nil}} {
		if _, err := answer(input); err == nil {
			t.Fatal("invalid Lua message accepted")
		}
	}
}

func TestActionRejectsBoardTokenFragments(t *testing.T) {
	for _, fragment := range []string{strings.Repeat("a", 64), strings.Repeat("B", 64), "%61" + strings.Repeat("a", 63)} {
		for _, input := range []any{map[string]any{"url": "https://example.test/#" + fragment}, mustJSON(t, map[string]any{"url": "https://example.test/#" + fragment})} {
			if _, err := answer(input); err == nil {
				t.Fatal("board login token fragment accepted")
			}
		}
	}
}

func TestActionRejectsNormalizedBootstrap(t *testing.T) {
	for _, p := range []string{"/a/../_omo/proxy/bootstrap", "/a/%2e%2e/_omo/proxy/bootstrap", "/%5fomo/proxy/bootstrap", "//_omo/proxy/bootstrap", "/_omo/proxy"} {
		if _, err := answer(map[string]any{"url": "https://example.test" + p + "#" + strings.Repeat("x", 43)}); err == nil {
			t.Fatalf("bootstrap accepted: %s", p)
		}
	}
}
