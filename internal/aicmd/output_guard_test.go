package aicmd

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// spoofText is untrusted text that tries to start a line of its own, steer
// the terminal and reorder what follows.
const spoofText = "a\nINJ\r\x1b[2J‮INJ INJ"

// spoofed returns a value of T with every plain string field, at any depth
// (structs, pointers to structs, one element of each slice and map),
// holding spoofText. Named string types (enums omo itself sets) are left.
func spoofed[T any]() T {
	var v T
	fill(reflect.ValueOf(&v).Elem(), 0)
	return v
}

func fill(v reflect.Value, depth int) {
	if depth > 6 {
		return
	}
	switch v.Kind() {
	case reflect.String:
		if v.Type() == reflect.TypeOf("") {
			v.SetString(spoofText)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i), depth+1)
			}
		}
	case reflect.Pointer:
		if v.Type().Elem().Kind() == reflect.Struct {
			v.Set(reflect.New(v.Type().Elem()))
			fill(v.Elem(), depth+1)
		}
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), depth+1)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fill(k, depth+1)
		fill(e, depth+1)
		m.SetMapIndex(k, e)
		v.Set(m)
	}
}

// TestPrintedTextStaysOnItsLine prints every kind of board data the CLI
// shows with spoofText in every text field: nothing of it may start a line,
// and no control, bidi or line separator character may reach the terminal.
func TestPrintedTextStaysOnItsLine(t *testing.T) {
	printers := map[string]func(w io.Writer){
		"project":  func(w io.Writer) { io.WriteString(w, projectLine(spoofed[api.Project]())+"\n") },
		"model":    func(w io.Writer) { io.WriteString(w, modelLine(spoofed[api.Model]())+"\n") },
		"ticket":   func(w io.Writer) { io.WriteString(w, ticketLine(spoofed[api.TicketSummary]())+"\n") },
		"show":     func(w io.Writer) { showTicket(w, spoofed[api.Ticket]()) },
		"comment":  func(w io.Writer) { printComment(w, spoofed[api.Comment]()) },
		"whoami":   func(w io.Writer) { printMe(w, spoofed[api.Me]()) },
		"no reach": func(w io.Writer) { unreachableLines(w, 1, []api.Role{api.RolePM}) },
		"forward":  func(w io.Writer) { printProxyForward(w, spoofed[api.ProxyForward]()) },
	}
	for name, print := range printers {
		var out bytes.Buffer
		print(&out)
		got := out.String()
		if strings.ContainsAny(got, "\r\x1b‮ ") {
			t.Errorf("%s: a hidden character reached the output: %q", name, got)
		}
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, "INJ") {
				t.Errorf("%s: untrusted text starts a line: %q", name, got)
			}
		}
	}
}
