package luabox

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	lua "github.com/yuin/gopher-lua"
)

// FromGo converts a Go value into a Lua value of L: nil, bool, string,
// []byte (as a string), the integer and float types, []any, []string,
// map[string]any, map[string]string, map[string][]string, a lua.LValue as
// it is, and any other value through its JSON form. Nesting deeper than
// MaxDepth, more than MaxItems values and values JSON cannot encode are
// refused.
func FromGo(L *lua.LState, v any) (lua.LValue, error) {
	c := &converter{}
	return c.fromGo(L, v, 0)
}

type converter struct {
	items int
}

func (c *converter) count() error {
	c.items++
	if c.items > MaxItems {
		return fmt.Errorf("value has more than %d entries", MaxItems)
	}
	return nil
}

func (c *converter) fromGo(L *lua.LState, v any, depth int) (lua.LValue, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("value nested deeper than %d levels", MaxDepth)
	}
	if err := c.count(); err != nil {
		return nil, err
	}
	switch v := v.(type) {
	case nil:
		return lua.LNil, nil
	case lua.LValue:
		return v, nil
	case bool:
		return lua.LBool(v), nil
	case string:
		return lua.LString(v), nil
	case []byte:
		return lua.LString(v), nil
	case int:
		return lua.LNumber(v), nil
	case int8:
		return lua.LNumber(v), nil
	case int16:
		return lua.LNumber(v), nil
	case int32:
		return lua.LNumber(v), nil
	case int64:
		return lua.LNumber(v), nil
	case uint:
		return lua.LNumber(v), nil
	case uint8:
		return lua.LNumber(v), nil
	case uint16:
		return lua.LNumber(v), nil
	case uint32:
		return lua.LNumber(v), nil
	case uint64:
		return lua.LNumber(v), nil
	case float32:
		return lua.LNumber(v), nil
	case float64:
		return lua.LNumber(v), nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil, fmt.Errorf("number out of range")
		}
		return lua.LNumber(f), nil
	case []any:
		t := L.CreateTable(len(v), 0)
		for _, item := range v {
			lv, err := c.fromGo(L, item, depth+1)
			if err != nil {
				return nil, err
			}
			t.Append(lv)
		}
		return t, nil
	case []string:
		t := L.CreateTable(len(v), 0)
		for _, item := range v {
			if err := c.count(); err != nil {
				return nil, err
			}
			t.Append(lua.LString(item))
		}
		return t, nil
	case map[string]any:
		t := L.CreateTable(0, len(v))
		for k, item := range v {
			lv, err := c.fromGo(L, item, depth+1)
			if err != nil {
				return nil, err
			}
			t.RawSetString(k, lv)
		}
		return t, nil
	case map[string]string:
		t := L.CreateTable(0, len(v))
		for k, item := range v {
			if err := c.count(); err != nil {
				return nil, err
			}
			t.RawSetString(k, lua.LString(item))
		}
		return t, nil
	case map[string][]string:
		t := L.CreateTable(0, len(v))
		for k, item := range v {
			lv, err := c.fromGo(L, item, depth+1)
			if err != nil {
				return nil, err
			}
			t.RawSetString(k, lv)
		}
		return t, nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("value of type %T cannot be converted", v)
		}
		var generic any
		if err := json.Unmarshal(raw, &generic); err != nil {
			return nil, fmt.Errorf("value of type %T cannot be converted", v)
		}
		c.items--
		return c.fromGo(L, generic, depth)
	}
}

// ToGo converts a Lua value into nil, bool, float64, string, []any (a table
// whose keys are exactly 1..n, and the empty table) or map[string]any (any
// other table; number keys become their decimal text). It reads tables raw,
// so no Lua code runs. Functions, coroutines, userdata, keys that are not
// strings or numbers, NaN and infinite numbers as keys, cycles, nesting
// deeper than MaxDepth and more than MaxItems values are refused.
func ToGo(v lua.LValue) (any, error) {
	c := &converter{}
	return c.toGo(v, 0, map[*lua.LTable]bool{})
}

func (c *converter) toGo(v lua.LValue, depth int, path map[*lua.LTable]bool) (any, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("value nested deeper than %d levels", MaxDepth)
	}
	if err := c.count(); err != nil {
		return nil, err
	}
	switch v := v.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(v), nil
	case lua.LNumber:
		return float64(v), nil
	case lua.LString:
		return string(v), nil
	case *lua.LTable:
		if path[v] {
			return nil, fmt.Errorf("table refers to itself")
		}
		path[v] = true
		defer delete(path, v)
		return c.table(v, depth, path)
	default:
		return nil, fmt.Errorf("a %s cannot be converted", v.Type().String())
	}
}

func (c *converter) table(t *lua.LTable, depth int, path map[*lua.LTable]bool) (any, error) {
	type entry struct {
		key   lua.LValue
		value lua.LValue
	}
	var entries []entry
	var keyErr error
	t.ForEach(func(k, val lua.LValue) {
		if keyErr != nil {
			return
		}
		if len(entries) >= MaxItems {
			keyErr = fmt.Errorf("value has more than %d entries", MaxItems)
			return
		}
		entries = append(entries, entry{k, val})
	})
	if keyErr != nil {
		return nil, keyErr
	}
	// An array: the keys are exactly 1..n.
	isArray := true
	for _, e := range entries {
		n, ok := e.key.(lua.LNumber)
		if !ok || float64(n) != math.Trunc(float64(n)) || n < 1 || int(n) > len(entries) {
			isArray = false
			break
		}
	}
	if isArray {
		out := make([]any, len(entries))
		for _, e := range entries {
			item, err := c.toGo(e.value, depth+1, path)
			if err != nil {
				return nil, err
			}
			out[int(e.key.(lua.LNumber))-1] = item
		}
		return out, nil
	}
	out := make(map[string]any, len(entries))
	for _, e := range entries {
		var key string
		switch k := e.key.(type) {
		case lua.LString:
			key = string(k)
		case lua.LNumber:
			f := float64(k)
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, fmt.Errorf("table key is not a finite number")
			}
			key = strconv.FormatFloat(f, 'f', -1, 64)
		default:
			return nil, fmt.Errorf("table key of type %s cannot be converted", e.key.Type().String())
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("table has a key twice, as a string and as a number")
		}
		item, err := c.toGo(e.value, depth+1, path)
		if err != nil {
			return nil, err
		}
		out[key] = item
	}
	return out, nil
}
