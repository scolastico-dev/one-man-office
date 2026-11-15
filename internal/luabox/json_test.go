package luabox

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestJSONRoundTrip(t *testing.T) {
	for _, text := range []string{
		`{"a":1,"b":[true,false,"x"],"c":{"d":"<e>"}}`,
		`[]`,
		`"text"`,
		`1.5`,
		`[1,2,3]`,
	} {
		if got := eval(t, `omo.json_encode(omo.json_decode('`+text+`'))`); got != text {
			t.Errorf("%s -> %v", text, got)
		}
	}
	for expr, want := range map[string]any{
		`select(2, omo.json_decode("{"))`:                                              phraseJSONInvalid,
		`select(2, omo.json_decode("[1] x"))`:                                          phraseJSONInvalid,
		`select(2, omo.json_decode(("["):rep(65) .. ("]"):rep(65)))`:                   phraseJSONTooDeep,
		`select(2, omo.json_decode(("x"):rep(1048576) .. "x"))`:                        phraseJSONTooLarge,
		`omo.json_decode(("["):rep(64) .. ("]"):rep(64)) ~= nil`:                       true,
		`select(2, omo.json_encode({f = print}))`:                                      phraseJSONEncode,
		`select(2, omo.json_encode(0/0))`:                                              phraseJSONEncode,
		`select(2, omo.json_encode(1/0))`:                                              phraseJSONEncode,
		`select(2, (function() local t = {} t.t = t return omo.json_encode(t) end)())`: phraseJSONEncode,
		`omo.json_decode('{"a":null}').a == nil`:                                       true,
		`omo.sha256_hex("abc")`:                                                        "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		`omo.nfc("e\204\129") == "\195\169"`:                                           true,
		`omo.time_parse("2026-11-15T18:00:00Z")`:                                       1794765600.0,
		`select(2, omo.time_parse("tomorrow"))`:                                        "invalid time",
		`omo.now() > 1700000000`:                                                       true,
	} {
		if got := eval(t, expr); got != want {
			t.Errorf("%s = %#v, want %#v", expr, got, want)
		}
	}
}

func TestConvertDepthAndCycles(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	// Go to Lua: depth.
	var deep any = "leaf"
	for i := 0; i < MaxDepth+2; i++ {
		deep = []any{deep}
	}
	if _, err := FromGo(L, deep); err == nil {
		t.Error("FromGo took a value deeper than MaxDepth")
	}
	if _, err := FromGo(L, make([]string, MaxItems+1)); err == nil {
		t.Error("FromGo took more than MaxItems values")
	}
	if _, err := FromGo(L, func() {}); err == nil {
		t.Error("FromGo took a function")
	}
	type rec struct {
		A string `json:"a"`
	}
	lv, err := FromGo(L, rec{A: "x"})
	if err != nil || lv.(*lua.LTable).RawGetString("a") != lua.LString("x") {
		t.Errorf("struct: %v %v", lv, err)
	}

	// Lua to Go: cycles, depth, kinds.
	b := New(context.Background(), Limits{})
	defer b.Close()
	if err := b.Load(`
		function cycle() local t = {} t.self = t return t end
		function shared() local s = {1} return {a = s, b = s} end
		function deep() local t = "x" for i = 1, 70 do t = {t} end return t end
		function mixed() return {1, 2, x = 3, [1.5] = 4} end
		function holes() return {1, nil, 3} end
		function badkey() return {[{}] = 1} end
		function dup() return {[1] = "a", ["1"] = "b", x = 1} end
		function co() return coroutine.create(function() end) end
		function nan() return {[0/0 + 0] = 1} end
	`, "conv"); err != nil {
		t.Fatal(err)
	}
	for fn, want := range map[string]string{
		"cycle": "refers to itself", "deep": "deeper", "badkey": "key of type", "dup": "twice", "co": "cannot be converted",
	} {
		if _, err := b.Call(fn); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", fn, err)
		}
	}
	v, err := b.Call("shared")
	if err != nil {
		t.Fatalf("shared table is not a cycle: %v", err)
	}
	if !reflect.DeepEqual(v, map[string]any{"a": []any{1.0}, "b": []any{1.0}}) {
		t.Errorf("shared: %#v", v)
	}
	v, _ = b.Call("mixed")
	if !reflect.DeepEqual(v, map[string]any{"1": 1.0, "2": 2.0, "x": 3.0, "1.5": 4.0}) {
		t.Errorf("mixed: %#v", v)
	}
	v, _ = b.Call("holes")
	if !reflect.DeepEqual(v, map[string]any{"1": 1.0, "3": 3.0}) {
		t.Errorf("holes: %#v", v)
	}
	if _, err := ToGo(lua.LNumber(math.Inf(1))); err != nil {
		t.Errorf("infinite number value: %v", err)
	}
}
