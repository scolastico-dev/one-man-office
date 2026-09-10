package tui

import (
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHitMapAddAndAtUsesHalfOpenRectangles(t *testing.T) {
	h := &hitMap{}
	action := keyAction{key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}}
	h.add(2, 3, 4, 5, action)

	for _, tc := range []struct {
		name string
		x, y int
		want bool
	}{
		{name: "top left", x: 2, y: 3, want: true},
		{name: "inside", x: 5, y: 7, want: true},
		{name: "right edge", x: 6, y: 4, want: false},
		{name: "bottom edge", x: 3, y: 8, want: false},
		{name: "before left", x: 1, y: 4, want: false},
		{name: "before top", x: 3, y: 2, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := h.at(tc.x, tc.y)
			if ok != tc.want {
				t.Fatalf("at(%d, %d) ok = %v, want %v", tc.x, tc.y, ok, tc.want)
			}
			if tc.want && !reflect.DeepEqual(got, action) {
				t.Fatalf("at(%d, %d) action = %#v, want %#v", tc.x, tc.y, got, action)
			}
		})
	}
}

func TestHitMapAtRejectsNegativeAndOutOfRangeCoordinates(t *testing.T) {
	h := &hitMap{}
	action := keyAction{key: tea.KeyMsg{Type: tea.KeyEnter}}
	h.add(0, 0, 3, 3, action)

	for _, tc := range [][2]int{{-1, 0}, {0, -1}, {-1, -1}, {3, 0}, {0, 3}} {
		if _, ok := h.at(tc[0], tc[1]); ok {
			t.Fatalf("at(%d, %d) matched outside rectangle", tc[0], tc[1])
		}
	}
}

func TestHitMapAddIgnoresNonPositiveDimensions(t *testing.T) {
	h := &hitMap{}
	action := keyAction{key: tea.KeyMsg{Type: tea.KeyEnter}}
	for _, tc := range []struct {
		x, y, w, h int
	}{
		{x: 1, y: 1, w: 0, h: 2},
		{x: 1, y: 1, w: 2, h: 0},
		{x: 1, y: 1, w: -1, h: 2},
		{x: 1, y: 1, w: 2, h: -1},
	} {
		h.add(tc.x, tc.y, tc.w, tc.h, action)
	}
	if _, ok := h.at(1, 1); ok {
		t.Fatal("non-positive rectangle matched")
	}
}

func TestHitMapLaterOverlappingRegistrationWins(t *testing.T) {
	h := &hitMap{}
	first := keyAction{key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}}
	second := keyAction{key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")}}
	h.add(1, 1, 4, 4, first)
	h.add(2, 2, 2, 2, second)

	if got, ok := h.at(2, 2); !ok || !reflect.DeepEqual(got, second) {
		t.Fatalf("overlap action = %#v, %v, want %#v, true", got, ok, second)
	}
	if got, ok := h.at(1, 1); !ok || !reflect.DeepEqual(got, first) {
		t.Fatalf("non-overlap action = %#v, %v, want %#v, true", got, ok, first)
	}
}

func TestHitMapResetRemovesRegistrations(t *testing.T) {
	h := &hitMap{}
	h.add(0, 0, 2, 2, keyAction{key: tea.KeyMsg{Type: tea.KeyEnter}})
	h.reset()
	if _, ok := h.at(0, 0); ok {
		t.Fatal("reset left a registration behind")
	}
}
