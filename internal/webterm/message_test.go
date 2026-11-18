package webterm

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// messageTerminal is a process that takes whole messages.
type messageTerminal struct {
	*controlledTerminal
	mu       sync.Mutex
	messages [][2]string
	err      error
}

func (m *messageTerminal) SendMessage(text, keys string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, [2]string{text, keys})
	return m.err
}

func (m *messageTerminal) got() [][2]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][2]string(nil), m.messages...)
}

func dialTerminal(t *testing.T, ctx context.Context, i *Terminal) *websocket.Conn {
	t.Helper()
	server := serveTerminal(t, i)
	c, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/terminal", &websocket.DialOptions{Subprotocols: []string{"omo"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	// Whatever history there is comes first; the end-of-replay frame closes it.
	for {
		kind, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("end of replay: %v", err)
		}
		if kind == websocket.MessageText && string(data) == replayEnd {
			return c
		}
	}
}

func readAck(t *testing.T, ctx context.Context, c *websocket.Conn) (id int64, errText string) {
	t.Helper()
	kind, data, err := c.Read(ctx)
	if err != nil || kind != websocket.MessageText {
		t.Fatalf("ack: %v %q %v", kind, data, err)
	}
	var ack struct {
		Type  string `json:"type"`
		ID    int64  `json:"id"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &ack); err != nil || ack.Type != "message-ack" {
		t.Fatalf("not a message ack: %q", data)
	}
	return ack.ID, ack.Error
}

func TestTerminalMessageIsOneWholeWriteWithItsKeys(t *testing.T) {
	p := &messageTerminal{controlledTerminal: terminalFixture(t)}
	i := Own("message", p, nil)
	// The program asked for bracketed paste: omo wraps the message itself, at the
	// moment it writes it, from what the program has asked for by then.
	i.publish([]byte("\x1b[?2004h"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialTerminal(t, ctx, i)
	frame, _ := json.Marshal(map[string]any{"type": "message", "id": 7, "text": "line one\nline two", "keys": "\r"})
	if err := c.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	if id, e := readAck(t, ctx, c); id != 7 || e != "" {
		t.Fatalf("ack = %d %q", id, e)
	}
	got := p.got()
	if want := "\x1b[200~line one\rline two\x1b[201~"; len(got) != 1 || got[0][0] != want || got[0][1] != "\r" {
		t.Fatalf("message = %q", got)
	}
}

func TestTerminalMessageFailureIsAnsweredAndKeepsTheConnection(t *testing.T) {
	p := &messageTerminal{controlledTerminal: terminalFixture(t), err: context.DeadlineExceeded}
	i := Own("message-fail", p, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialTerminal(t, ctx, i)
	frame, _ := json.Marshal(map[string]any{"type": "message", "id": 3, "text": "hello", "keys": "\r"})
	if err := c.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	if id, e := readAck(t, ctx, c); id != 3 || e == "" {
		t.Fatalf("ack = %d %q, want an error", id, e)
	}
	// The connection still takes output.
	i.publish([]byte("alive"))
	kind, data, err := c.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(data) != "alive" {
		t.Fatalf("after a failed message: %v %q %v", kind, data, err)
	}
}

func TestTerminalMessageTooLargeIsRefusedInTheAck(t *testing.T) {
	p := &messageTerminal{controlledTerminal: terminalFixture(t)}
	i := Own("message-big", p, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialTerminal(t, ctx, i)
	frame, _ := json.Marshal(map[string]any{"type": "message", "id": 1, "text": strings.Repeat("a", terminalInputLimit+1), "keys": "\r"})
	if err := c.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	if id, e := readAck(t, ctx, c); id != 1 || e == "" {
		t.Fatalf("ack = %d %q, want a refusal", id, e)
	}
	if len(p.got()) != 0 {
		t.Fatal("an oversized message was written")
	}
}

// recordingTerminal takes every write.
type recordingTerminal struct {
	*controlledTerminal
	mu     sync.Mutex
	writes []string
}

func (r *recordingTerminal) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, string(b))
	return len(b), nil
}

func TestTerminalMessageOnAProcessWithoutMessagesWritesTextThenKeys(t *testing.T) {
	p := &recordingTerminal{controlledTerminal: terminalFixture(t)}
	i := Own("message-plain", p, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialTerminal(t, ctx, i)
	frame, _ := json.Marshal(map[string]any{"type": "message", "id": 2, "text": "hi", "keys": "\r"})
	if err := c.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	if id, e := readAck(t, ctx, c); id != 2 || e != "" {
		t.Fatalf("ack = %d %q", id, e)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.Join(p.writes, "|") != "hi|\r" {
		t.Fatalf("writes = %q", p.writes)
	}
}

func TestTerminalMessageWithoutBracketedPasteIsPlainTextWithEnterPresses(t *testing.T) {
	p := &messageTerminal{controlledTerminal: terminalFixture(t)}
	i := Own("message-nobracket", p, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialTerminal(t, ctx, i)
	frame, _ := json.Marshal(map[string]any{"type": "message", "id": 1, "text": "one\r\ntwo\nthree", "keys": "\r"})
	if err := c.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	readAck(t, ctx, c)
	if got := p.got(); len(got) != 1 || got[0][0] != "one\rtwo\rthree" {
		t.Fatalf("message = %q", got)
	}
}

func TestTerminalMessageNeverCarriesControlCharactersOrItsOwnPasteEnd(t *testing.T) {
	p := &messageTerminal{controlledTerminal: terminalFixture(t)}
	i := Own("message-clean", p, nil)
	i.publish([]byte("\x1b[?2004h"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialTerminal(t, ctx, i)
	frame, _ := json.Marshal(map[string]any{"type": "message", "id": 1, "text": "a\x1b[201~rm -rf\x03b\ttab\u009b", "keys": "\r"})
	if err := c.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	readAck(t, ctx, c)
	if got := p.got(); len(got) != 1 || got[0][0] != "\x1b[200~a[201~rm -rfb\ttab\x1b[201~" {
		t.Fatalf("message = %q", got)
	}
}

func TestTerminalMessageNeedsAPositiveIDAndIsOtherwiseIgnored(t *testing.T) {
	p := &messageTerminal{controlledTerminal: terminalFixture(t)}
	i := Own("message-id", p, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialTerminal(t, ctx, i)
	for _, id := range []int{0, -4} {
		frame, _ := json.Marshal(map[string]any{"type": "message", "id": id, "text": "no", "keys": "\r"})
		if err := c.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
	}
	frame, _ := json.Marshal(map[string]any{"type": "message", "id": 5, "text": "yes", "keys": "\r"})
	if err := c.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	// The first answer is for message 5: no input-ack came for the others.
	if id, e := readAck(t, ctx, c); id != 5 || e != "" {
		t.Fatalf("ack = %d %q", id, e)
	}
	if got := p.got(); len(got) != 1 || got[0][0] != "yes" {
		t.Fatalf("messages = %q", got)
	}
}
