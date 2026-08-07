package tui

import tea "github.com/charmbracelet/bubbletea"

// keyToBytes encodes a Bubble Tea key event as the raw bytes a terminal
// would send, for passthrough into an agent's PTY.
func keyToBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	}
	if b, ok := ctrlBytes[msg.Type]; ok {
		return []byte{b}
	}
	return nil
}

// ctrlBytes maps the Ctrl+A…Ctrl+Z key types onto their control codes.
// An explicit map rather than arithmetic on the constants: bubbletea does
// not guarantee they are contiguous or ordered alphabetically.
var ctrlBytes = map[tea.KeyType]byte{
	tea.KeyCtrlA: 0x01, tea.KeyCtrlB: 0x02, tea.KeyCtrlC: 0x03, tea.KeyCtrlD: 0x04,
	tea.KeyCtrlE: 0x05, tea.KeyCtrlF: 0x06, tea.KeyCtrlG: 0x07, tea.KeyCtrlH: 0x08,
	tea.KeyCtrlJ: 0x0a, tea.KeyCtrlK: 0x0b, tea.KeyCtrlL: 0x0c,
	tea.KeyCtrlN: 0x0e, tea.KeyCtrlO: 0x0f, tea.KeyCtrlP: 0x10,
	tea.KeyCtrlQ: 0x11, tea.KeyCtrlR: 0x12, tea.KeyCtrlS: 0x13, tea.KeyCtrlT: 0x14,
	tea.KeyCtrlU: 0x15, tea.KeyCtrlV: 0x16, tea.KeyCtrlW: 0x17, tea.KeyCtrlX: 0x18,
	tea.KeyCtrlY: 0x19, tea.KeyCtrlZ: 0x1a,
}
