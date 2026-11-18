package plugins

import (
	"bytes"
	"encoding/json"
	"net"
	"net/url"

	"github.com/scolastico-dev/one-man-office/internal/luabox"
	lua "github.com/yuin/gopher-lua"
)

func (m *Manager) bindForwardHelpers(box *luabox.Box, p *plugin) {
	live := func(L *lua.LState) bool {
		if box.Context().Err() != nil || p.ctx.Err() != nil {
			fail(L, "the plugin hook has stopped")
			return false
		}
		if m.o.Forwards == nil {
			fail(L, "forwards are unavailable")
			return false
		}
		return true
	}
	box.Set("forward_add", func(L *lua.LState) int {
		p.forwardMu.Lock()
		defer p.forwardMu.Unlock()
		if !live(L) {
			return 2
		}
		value, err := luabox.ToGo(L.CheckTable(1))
		if err != nil {
			return fail(L, "forward_add requires an options table")
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return fail(L, "invalid forward options")
		}
		var req ForwardAdd
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil {
			return fail(L, "forward_add has an unknown field or a value of the wrong type")
		}
		// Registry destinations may contain application state, never credentials.
		path := req.StartPath
		if path == "" {
			path = "/"
		}
		decoded, _ := url.QueryUnescape(path)
		red := box.Redactor()
		if !validActionURL("http://forward.invalid"+path) || red.Clean(path) != path || red.Clean(decoded) != decoded || red.Clean(req.Name) != req.Name || red.Clean(req.Host) != req.Host {
			return fail(L, "forward options contain credentials or hidden characters")
		}
		out, err := m.o.Forwards.AddForward(box.Context(), p.name, req)
		if err != nil {
			return fail(L, red.Clean(err.Error()))
		}
		return pushTaskValue(L, out)
	})
	box.Set("forward_list", func(L *lua.LState) int {
		p.forwardMu.Lock()
		defer p.forwardMu.Unlock()
		if !live(L) {
			return 2
		}
		out, err := m.o.Forwards.ListForwards(box.Context(), p.name)
		if err != nil {
			return fail(L, "cannot list plugin forwards")
		}
		return pushTaskValue(L, out)
	})
	box.Set("forward_remove", func(L *lua.LState) int {
		p.forwardMu.Lock()
		defer p.forwardMu.Unlock()
		if !live(L) {
			return 2
		}
		if err := m.o.Forwards.RemoveForward(box.Context(), p.name, L.CheckString(1)); err != nil {
			return fail(L, "plugin forward does not exist or cannot be removed")
		}
		L.Push(lua.LTrue)
		return 1
	})
	box.Set("forward_attach", func(L *lua.LState) int {
		p.forwardMu.Lock()
		defer p.forwardMu.Unlock()
		if !live(L) {
			return 2
		}
		if err := m.o.Forwards.AttachForward(box.Context(), p.name, L.CheckString(1), L.CheckString(2)); err != nil {
			return fail(L, "cannot attach forward to this running task")
		}
		L.Push(lua.LTrue)
		return 1
	})
	box.Set("forward_idle_watch", func(L *lua.LState) int {
		p.forwardMu.Lock()
		defer p.forwardMu.Unlock()
		if !live(L) {
			return 2
		}
		if err := m.o.Forwards.WatchForward(box.Context(), p.name, L.CheckString(1), float64(L.CheckNumber(2))); err != nil {
			return fail(L, "idle watch requires an owned running task and 0.01 to 604800 seconds")
		}
		L.Push(lua.LTrue)
		return 1
	})
	box.Set("free_loopback_port", func(L *lua.LState) int {
		// This is a candidate, not a reservation. The plugin retries bind collisions.
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return fail(L, "cannot find a free loopback port")
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if listener.Close() != nil {
			return fail(L, "cannot release loopback port candidate")
		}
		L.Push(lua.LNumber(port))
		return 1
	})
}
