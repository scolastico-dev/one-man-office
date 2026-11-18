package plugins

import (
	"bytes"
	"encoding/json"
	"net/url"
	"runtime"

	"github.com/scolastico-dev/one-man-office/internal/luabox"
	lua "github.com/yuin/gopher-lua"
)

func artifactAddressSecrets(red *luabox.Redactor, address string) {
	red.Add(address)
	if u, err := url.Parse(address); err == nil {
		if u.User != nil {
			red.Add(u.User.Username())
			if password, ok := u.User.Password(); ok {
				red.Add(password)
			}
		}
		for _, values := range u.Query() {
			for _, v := range values {
				red.Add(v)
			}
		}
	}
}
func pushArtifactValue(L *lua.LState, v any) int {
	value, err := luabox.FromGo(L, v)
	if err != nil {
		return fail(L, "the artifact result exceeds Lua limits")
	}
	L.Push(value)
	return 1
}
func (m *Manager) luaArtifactStart(box *luabox.Box, p *plugin) lua.LGFunction {
	return func(L *lua.LState) int {
		value, err := luabox.ToGo(L.CheckTable(1))
		if err != nil {
			return fail(L, "artifact_start requires an options table")
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return fail(L, "invalid artifact options")
		}
		var req ArtifactStart
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&req); err != nil {
			return fail(L, "artifact_start has an unknown field or a value of the wrong type")
		}
		artifactAddressSecrets(box.Redactor(), req.URL)
		status, err := m.startArtifact(box.Context(), p.name, p, req)
		if err != nil {
			return fail(L, err.Error())
		}
		return pushArtifactValue(L, status)
	}
}
func (m *Manager) luaArtifactStatus(p *plugin) lua.LGFunction {
	return func(L *lua.LState) int {
		status, err := m.ArtifactStatus(p.name, L.CheckString(1))
		if err != nil {
			return fail(L, err.Error())
		}
		return pushArtifactValue(L, status)
	}
}
func luaRuntimeInfo(L *lua.LState) int {
	return pushArtifactValue(L, map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH})
}
