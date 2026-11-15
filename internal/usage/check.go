package usage

import (
	"context"
	"errors"
	"fmt"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/scolastico-dev/one-man-office/internal/luabox"
)

// MaxScript is the largest usage script.
const MaxScript = luabox.DefaultMaxSource

// checkTimeout bounds the top level of a script Check runs.
const checkTimeout = 2 * time.Second

// Check tells whether text can be saved as the usage script name: at most
// MaxScript bytes, it compiles, its top level runs in a box without calling
// any helper that reaches outside (omo.http, read_file, env, keychain,
// codex_refresh), within 2 s, and afterwards a global function usage
// exists. The error is one line without the script's source.
func Check(name, text string) error {
	if len(text) > MaxScript {
		return fmt.Errorf("the script exceeds %d bytes", MaxScript)
	}
	box := luabox.New(context.Background(), luabox.Limits{Timeout: checkTimeout})
	defer box.Close()
	called := false
	for _, helper := range runHelpers {
		msg := "omo." + helper + " cannot be called at the top level of a script; call it inside usage"
		box.Set(helper, func(L *lua.LState) int {
			called = true
			L.RaiseError("%s", msg)
			return 0
		})
	}
	if err := box.Load(text, chunkName(name)); err != nil {
		return errors.New(luabox.ErrorText(err, box.Redactor()))
	}
	if called {
		return errors.New("the top level of the script calls a helper that reaches outside; call helpers inside usage")
	}
	if _, ok := box.L.GetGlobal("usage").(*lua.LFunction); !ok {
		return errors.New("the script does not define a function usage")
	}
	return nil
}
