package tui

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestKeyToBytes(t *testing.T) {
	cases := []struct {
		msg  tea.KeyMsg
		want []byte
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")}, []byte("hi")},
		{tea.KeyMsg{Type: tea.KeyEnter}, []byte("\r")},
		{tea.KeyMsg{Type: tea.KeyBackspace}, []byte{0x7f}},
		{tea.KeyMsg{Type: tea.KeyTab}, []byte("\t")},
		{tea.KeyMsg{Type: tea.KeyEsc}, []byte{0x1b}},
		{tea.KeyMsg{Type: tea.KeySpace}, []byte(" ")},
		{tea.KeyMsg{Type: tea.KeyUp}, []byte("\x1b[A")},
		{tea.KeyMsg{Type: tea.KeyDown}, []byte("\x1b[B")},
		{tea.KeyMsg{Type: tea.KeyRight}, []byte("\x1b[C")},
		{tea.KeyMsg{Type: tea.KeyLeft}, []byte("\x1b[D")},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, []byte{0x03}},
		{tea.KeyMsg{Type: tea.KeyCtrlD}, []byte{0x04}},
	}
	for _, c := range cases {
		if got := keyToBytes(c.msg); !bytes.Equal(got, c.want) {
			t.Errorf("%v → %q, want %q", c.msg, got, c.want)
		}
	}
}
