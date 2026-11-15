package api

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDurationText(t *testing.T) {
	for in, want := range map[time.Duration]string{
		15 * time.Minute: "15m", time.Hour: "1h", 90 * time.Minute: "1h30m", 90 * time.Second: "1m30s", 0: "0s",
	} {
		b, err := json.Marshal(Duration(in))
		if err != nil || string(b) != `"`+want+`"` {
			t.Errorf("%v: %s %v", in, b, err)
		}
		var d Duration
		if err := json.Unmarshal(b, &d); err != nil || time.Duration(d) != in {
			t.Errorf("%s: %v %v", b, d, err)
		}
	}
	var d Duration
	if err := json.Unmarshal([]byte(`"soon"`), &d); err == nil {
		t.Error("bad duration accepted")
	}
}
