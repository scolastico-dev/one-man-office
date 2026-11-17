package luabox

import (
	"time"

	lua "github.com/yuin/gopher-lua"
)

// MaxSleep bounds one omo.sleep.
const MaxSleep = 10 * time.Second

// Sleep is the omo.sleep helper bound to b: omo.sleep(seconds) waits that
// many seconds (more than 0, at most MaxSleep; fractions allowed) and
// returns true. It never waits past the run's end: when the run's time is
// up or the run is cancelled meanwhile, it raises the run's stop reason, as
// a busy loop would be stopped. It is for waiting before a retry, such as
// the Retry-After of an HTTP 429. Register it with b.Set("sleep", …).
func (b *Box) Sleep() lua.LGFunction {
	return func(L *lua.LState) int {
		n, ok := L.Get(1).(lua.LNumber)
		if !ok || L.GetTop() != 1 {
			L.RaiseError("omo.sleep takes a number of seconds")
		}
		d := time.Duration(float64(n) * float64(time.Second))
		if !(float64(n) > 0) || float64(n) > MaxSleep.Seconds() {
			L.RaiseError("omo.sleep takes more than 0 and at most %d seconds", int(MaxSleep.Seconds()))
		}
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-b.ctx.Done():
			b.checkStop(L)
		}
		L.Push(lua.LTrue)
		return 1
	}
}
