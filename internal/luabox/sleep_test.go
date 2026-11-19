package luabox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func sleepCall(t *testing.T, lim Limits, src string) (any, error) {
	t.Helper()
	b := New(context.Background(), lim)
	t.Cleanup(b.Close)
	b.Set("sleep", b.Sleep())
	if err := b.Load(src, "sleep"); err != nil {
		t.Fatal(err)
	}
	return b.Call("f")
}

func TestSleepWaits(t *testing.T) {
	start := time.Now()
	v, err := sleepCall(t, Limits{}, `function f() return omo.sleep(0.05) end`)
	if err != nil || v != true {
		t.Fatalf("v %v, err %v", v, err)
	}
	if took := time.Since(start); took < 50*time.Millisecond {
		t.Fatalf("slept %v", took)
	}
}

func TestSleepRefusesBadArguments(t *testing.T) {
	for _, arg := range []string{"0", "-1", "11", "0/0", "'1'", "", "1, 2", "math.huge"} {
		_, err := sleepCall(t, Limits{}, `function f() return omo.sleep(`+arg+`) end`)
		if err == nil || !strings.Contains(err.Error(), "omo.sleep takes") {
			t.Errorf("omo.sleep(%s): %v", arg, err)
		}
	}
}

// TestSleepEndsWithTheRun: a sleep longer than the time the run has left
// ends with the run's time limit, not after the sleep.
func TestSleepEndsWithTheRun(t *testing.T) {
	var err error
	completeWithin(t, 2*time.Second, func() {
		_, err = sleepCall(t, Limits{Timeout: 100 * time.Millisecond}, `function f() omo.sleep(10) return "woke" end`)
	})
	if err == nil || !strings.Contains(err.Error(), msgTimeLimit) {
		t.Fatalf("err %v", err)
	}
}
