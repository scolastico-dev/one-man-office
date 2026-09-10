package tui

import tea "github.com/charmbracelet/bubbletea"

// clickAction is the private vocabulary shared by rendered hit regions and
// the mouse dispatcher. Later views can add actions without exposing hit-map
// implementation details to Bubble Tea.
type clickAction interface {
	clickAction()
}

type keyAction struct {
	key tea.KeyMsg
}

func (keyAction) clickAction() {}

type tabAction struct {
	tab overviewTab
}

func (tabAction) clickAction() {}

// These action declarations reserve the typed vocabulary for later clickable
// overview rows, command inputs, command suggestions, and plugin actions.
type rowAction struct {
	tab overviewTab
	row int
}

func (rowAction) clickAction() {}

type inputAction struct {
	input int
}

func (inputAction) clickAction() {}

type suggestionAction struct {
	suggestion int
}

func (suggestionAction) clickAction() {}

type pluginActionAction struct {
	action string
}

func (pluginActionAction) clickAction() {}

type hitRect struct {
	x, y, w, h int
	action     clickAction
}

type hitMap struct {
	rects []hitRect
}

func (h *hitMap) reset() {
	if h != nil {
		h.rects = h.rects[:0]
	}
}

func (h *hitMap) add(x, y, w, height int, action clickAction) {
	if h == nil || w <= 0 || height <= 0 || action == nil {
		return
	}
	h.rects = append(h.rects, hitRect{x: x, y: y, w: w, h: height, action: action})
}

func (h *hitMap) at(x, y int) (clickAction, bool) {
	if h == nil {
		return nil, false
	}
	for i := len(h.rects) - 1; i >= 0; i-- {
		r := h.rects[i]
		if x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h {
			return r.action, true
		}
	}
	return nil, false
}
