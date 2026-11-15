package api

import (
	"encoding/json"
	"testing"
)

// A home shell request carries home and no ticket; the others leave home
// out.
func TestOpenTerminalRequestHome(t *testing.T) {
	for _, c := range []struct {
		req  OpenTerminalRequest
		want string
	}{
		{OpenTerminalRequest{Home: true}, `{"project":"","home":true}`},
		{OpenTerminalRequest{Project: "p"}, `{"project":"p"}`},
	} {
		raw, err := json.Marshal(c.req)
		if err != nil || string(raw) != c.want {
			t.Errorf("%+v = %s %v, want %s", c.req, raw, err, c.want)
		}
	}
	var req OpenTerminalRequest
	if err := json.Unmarshal([]byte(`{"home":true}`), &req); err != nil || !req.Home || req.Project != "" {
		t.Fatalf("decoded %+v %v", req, err)
	}
}
