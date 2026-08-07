package session

import (
	"reflect"
	"testing"
	"time"
)

func TestScreenLogDeltaDeduplicatesRedrawsAndDropsChrome(t *testing.T) {
	s := &Session{}
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	first := "Static header\n" +
		"❯ \n" +
		"─────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle)\n" +
		"✢ Bunning… (1s · ↓ 10 tokens)\n" +
		"● Running 1 shell command…\n" +
		"  ⎿  $ omo ready"
	if got, want := s.screenLogDelta(first, now), []string{
		"Static header", "● Running 1 shell command…", "  ⎿  $ omo ready",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first delta = %#v, want %#v", got, want)
	}

	// The CLI moves unchanged content to different rows, toggles the activity
	// bullet and advances its spinner. Only genuinely new content should land
	// in the transcript.
	second := "Running 1 shell command…\n" +
		"✶ Bunning… (2s · ↓ 20 tokens)\n" +
		"  ⎿  $ omo ready\n" +
		"A genuinely new result\n" +
		"Static header"
	if got, want := s.screenLogDelta(second, now.Add(time.Second)), []string{"A genuinely new result"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("redraw delta = %#v, want %#v", got, want)
	}
}

func TestScreenLogDeltaAllowsOldContentToRecur(t *testing.T) {
	s := &Session{}
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	if got := s.screenLogDelta("same result", now); len(got) != 1 {
		t.Fatalf("initial delta = %#v", got)
	}
	s.screenLogDelta("", now.Add(time.Second))
	if got := s.screenLogDelta("same result", now.Add(2*time.Second)); len(got) != 0 {
		t.Fatalf("recent duplicate was logged: %#v", got)
	}
	s.screenLogDelta("", now.Add(2*time.Minute))
	if got := s.screenLogDelta("same result", now.Add(2*time.Minute+time.Second)); len(got) != 1 {
		t.Fatalf("old meaningful content should be allowed again: %#v", got)
	}
}
