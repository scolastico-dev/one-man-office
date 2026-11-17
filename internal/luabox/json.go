package luabox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
	"golang.org/x/text/unicode/norm"
)

// Fixed phrases of the JSON helpers.
const (
	phraseJSONInvalid  = "invalid json"
	phraseJSONTooLarge = "json text exceeds 1048576 bytes"
	phraseJSONTooDeep  = "json nested deeper than 64 levels"
	phraseJSONEncode   = "value cannot be encoded as json"
)

var (
	errJSONTooDeep = errors.New(phraseJSONTooDeep)
	errJSONInvalid = errors.New(phraseJSONInvalid)
)

// DecodeJSON decodes text (at most MaxString bytes, nesting at most
// MaxDepth) into nil, bool, float64, string, []any and map[string]any. When
// strs is not nil, every string value (not object keys) is passed to it.
func DecodeJSON(text string, strs func(string)) (any, error) {
	if len(text) > MaxString {
		return nil, errors.New(phraseJSONTooLarge)
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	items := 0
	v, err := decodeValue(dec, 0, &items, strs)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errJSONInvalid
	}
	return v, nil
}

func decodeValue(dec *json.Decoder, depth int, items *int, strs func(string)) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, errJSONInvalid
	}
	*items++
	if *items > MaxItems {
		return nil, errJSONInvalid
	}
	switch t := tok.(type) {
	case json.Delim:
		if depth >= MaxDepth {
			return nil, errJSONTooDeep
		}
		switch t {
		case '[':
			list := []any{}
			for dec.More() {
				v, err := decodeValue(dec, depth+1, items, strs)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, errJSONInvalid
			}
			return list, nil
		case '{':
			obj := map[string]any{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, errJSONInvalid
				}
				key, ok := kt.(string)
				if !ok {
					return nil, errJSONInvalid
				}
				v, err := decodeValue(dec, depth+1, items, strs)
				if err != nil {
					return nil, err
				}
				obj[key] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, errJSONInvalid
			}
			return obj, nil
		}
		return nil, errJSONInvalid
	case string:
		if strs != nil {
			strs(t)
		}
		return t, nil
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return nil, errJSONInvalid
		}
		return f, nil
	default: // bool, nil
		return t, nil
	}
}

// EncodeJSON encodes a value ToGo returned, refusing NaN and infinite
// numbers and texts over MaxString. Object keys are sorted; HTML characters
// are not escaped.
func EncodeJSON(v any) (string, error) {
	if err := checkFinite(v, 0); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", errors.New(phraseJSONEncode)
	}
	out := strings.TrimSuffix(buf.String(), "\n")
	if len(out) > MaxString {
		return "", errors.New(phraseJSONTooLarge)
	}
	return out, nil
}

func checkFinite(v any, depth int) error {
	if depth > MaxDepth {
		return errors.New(phraseJSONEncode)
	}
	switch v := v.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New(phraseJSONEncode)
		}
	case []any:
		for _, item := range v {
			if err := checkFinite(item, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range v {
			if err := checkFinite(item, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// pushFail pushes nil and a fixed phrase, the way helpers fail.
func pushFail(L *lua.LState, phrase string) int {
	L.Push(lua.LNil)
	L.Push(lua.LString(phrase))
	return 2
}

// jsonDecode is omo.json_decode(text). Strings decoded from a text marked
// with MarkSource become secrets.
func (b *Box) jsonDecode(L *lua.LState) int {
	text := L.CheckString(1)
	var strs func(string)
	if b.red.isMarked(text) {
		strs = b.red.Add
	}
	v, err := DecodeJSON(text, strs)
	if err != nil {
		return pushFail(L, err.Error())
	}
	lv, err := FromGo(L, v)
	if err != nil {
		return pushFail(L, phraseJSONInvalid)
	}
	L.Push(lv)
	return 1
}

// jsonEncode is omo.json_encode(value).
func (b *Box) jsonEncode(L *lua.LState) int {
	v, err := ToGo(L.CheckAny(1))
	if err != nil {
		return pushFail(L, phraseJSONEncode)
	}
	out, err := EncodeJSON(v)
	if err != nil {
		return pushFail(L, err.Error())
	}
	L.Push(lua.LString(out))
	return 1
}

// luaSHA256 is omo.sha256_hex(text).
func luaSHA256(L *lua.LState) int {
	sum := sha256.Sum256([]byte(L.CheckString(1)))
	L.Push(lua.LString(hex.EncodeToString(sum[:])))
	return 1
}

// luaNFC is omo.nfc(text): the text in Unicode normalization form C.
func luaNFC(L *lua.LState) int {
	L.Push(lua.LString(norm.NFC.String(L.CheckString(1))))
	return 1
}

// luaTimeParse is omo.time_parse(rfc3339): Unix seconds, with fractions.
func luaTimeParse(L *lua.LState) int {
	t, err := time.Parse(time.RFC3339Nano, L.CheckString(1))
	if err != nil {
		return pushFail(L, "invalid time")
	}
	L.Push(lua.LNumber(float64(t.Unix()) + float64(t.Nanosecond())/1e9))
	return 1
}

// luaNow is omo.now(): whole Unix seconds.
func luaNow(L *lua.LState) int {
	L.Push(lua.LNumber(time.Now().Unix()))
	return 1
}
