package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUIWireAdditiveFeatures(t *testing.T) {
	// A client can read a newer server's flags and fields using these types.
	var got UICapabilities
	if err := json.Unmarshal([]byte(`{"features":["ui.notifications.poll.v1","future.unknown"],"future_field":true}`), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Features, []string{FeatureUINotificationPolling, "future.unknown"}) {
		t.Fatalf("features: %+v", got)
	}
}

func TestUIWireNotificationCursorPrecision(t *testing.T) {
	// Values beyond JavaScript's exact integer range must remain strings on the wire.
	const fixture = `{"notifications":[{"id":"event:9007199254740993","kind":"mention","title":"Mention on #1","body":"@user decide","destination":"/1?tab=comments&comment=2"}],"next_cursor":"9007199254740993","has_more":true}`
	var got UINotificationFeed
	if err := json.Unmarshal([]byte(fixture), &got); err != nil {
		t.Fatal(err)
	}
	if got.NextCursor != "9007199254740993" || !got.HasMore || len(got.Notifications) != 1 || got.Notifications[0].ID != "event:9007199254740993" || got.Notifications[0].Destination != "/1?tab=comments&comment=2" {
		t.Fatalf("feed: %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var wantJSON, gotJSON map[string]any
	if err := json.Unmarshal([]byte(fixture), &wantJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &gotJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("round trip: %s", raw)
	}
}
