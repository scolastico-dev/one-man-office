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

// The keys of the merge modes, as the web reads them.
func TestMergeModesKeys(t *testing.T) {
	marshal := func(v any) string {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	for _, c := range []struct {
		v    any
		want string
	}{
		{MergeModes{Story: MergeModeSquash}, `{"story":"squash","task":"","story_task":""}`},
		{UpdateProject{Merge: Some(MergeModesUpdate{Task: Some("")})}, `{"merge":{"task":""}}`},
	} {
		if got := marshal(c.v); got != c.want {
			t.Errorf("%T = %s, want %s", c.v, got, c.want)
		}
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal([]byte(marshal(Project{})), &p); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"merge", "effective_merge", "story_merge"} {
		if _, ok := p[k]; !ok {
			t.Errorf("Project lacks %q", k)
		}
	}
	if MergeModeMerge != "merge" || MergeModeSquash != "squash" {
		t.Error("values")
	}
}
