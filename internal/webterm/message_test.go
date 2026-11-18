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
	expectReplayEnd(t, ctx, c)
	return c
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
	p := &messageTerminal{controlledTerminal: terminalFixture()}
	i := Own("message", p, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialTerminal(t, ctx, i)
	text := "\x1b[200~line one\nline two\x1b[201~"
	frame, _ := json.Marshal(map[string]any{"type": "message", "id": 7, "text": text, "keys": "\r"})
	if err := c.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	if id, e := readAck(t, ctx, c); id != 7 || e != "" {
		t.Fatalf("ack = %d %q", id, e)
	}
	got := p.got()
	if len(got) != 1 || got[0][0] != text || got[0][1] != "\r" {
		t.Fatalf("message = %q", got)
	}
}

func TestTerminalMessageFailureIsAnsweredAndKeepsTheConnection(t *testing.T) {
	p := &messageTerminal{controlledTerminal: terminalFixture(), err: context.DeadlineExceeded}
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
	p := &messageTerminal{controlledTerminal: terminalFixture()}
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
	p := &recordingTerminal{controlledTerminal: terminalFixture()}
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
