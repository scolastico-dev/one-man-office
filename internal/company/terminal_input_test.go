package company

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type pasteTerminal struct {
	mu      sync.Mutex
	data    bytes.Buffer
	gate    chan struct{}
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (p *pasteTerminal) Read([]byte) (int, error) { <-p.closed; return 0, io.EOF }
func (p *pasteTerminal) Write(data []byte) (int, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	select {
	case <-p.gate:
	case <-p.closed:
		return 0, io.ErrClosedPipe
	}
	// Deliberately accept short writes, as an underlying terminal writer may do.
	n := min(len(data), 173)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data.Write(data[:n])
}
func (p *pasteTerminal) Resize(uint16, uint16) error { return nil }
func (p *pasteTerminal) Wait() error                 { <-p.closed; return nil }
func (p *pasteTerminal) Kill() error                 { return p.Close() }
func (p *pasteTerminal) Close() error                { p.once.Do(func() { close(p.closed) }); return nil }

func TestTerminalPasteAcknowledgesCompleteWritesAndKeepsConnection(t *testing.T) {
	s, server := testServer(t)
	p := &pasteTerminal{gate: make(chan struct{}), started: make(chan struct{}, 1), closed: make(chan struct{})}
	i := ownInstance("paste", "/test", "shell", p, nil)
	s.mu.Lock()
	s.instances["paste"] = i
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint.Scheme = "ws"
	endpoint.Path = "/api/instances/paste/terminal"
	c, _, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{Subprotocols: []string{"omo", "omo-token." + s.token}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	// Include bracketed-paste markers and UTF-8 characters split across frames.
	payload := []byte("\x1b[200~" + strings.Repeat("hello ä🎉\r", 12000) + "\x1b[201~")
	first := payload[:16<<10]
	if err := c.Write(ctx, websocket.MessageBinary, first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.started:
	case <-ctx.Done():
		t.Fatal("terminal write did not start")
	}
	// Output and resizes must still pass while the PTY is not consuming input.
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"rows":40,"cols":120}`)); err != nil {
		t.Fatal(err)
	}
	i.publish([]byte("still responsive"))
	kind, data, err := c.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(data) != "still responsive" {
		t.Fatalf("output before write completion: %v %q %v", kind, data, err)
	}
	close(p.gate)
	checkAck := func() {
		t.Helper()
		kind, data, err := c.Read(ctx)
		if err != nil || kind != websocket.MessageText || string(data) != `{"type":"input-ack"}` {
			t.Fatalf("write acknowledgment: %v %q %v", kind, data, err)
		}
	}
	checkAck()
	for offset := len(first); offset < len(payload); offset += 16 << 10 {
		if err := c.Write(ctx, websocket.MessageBinary, payload[offset:min(offset+(16<<10), len(payload))]); err != nil {
			t.Fatal(err)
		}
		checkAck()
	}
	p.mu.Lock()
	got := append([]byte(nil), p.data.Bytes()...)
	p.mu.Unlock()
	if !bytes.Equal(got, payload) {
		t.Fatalf("paste corrupted: got %d bytes, want %d", len(got), len(payload))
	}
	if err := c.Write(ctx, websocket.MessageBinary, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	checkAck()
	p.mu.Lock()
	defer p.mu.Unlock()
	if !bytes.Equal(p.data.Bytes(), append(payload, '\r')) {
		t.Fatal("subsequent Enter lost or reordered")
	}
}

func TestTerminalExitDrainsOutputAfterInputWriterStops(t *testing.T) {
	s, server := testServer(t)
	p := terminalFixture()
	i := ownInstance("tail", "/test", "shell", p, nil)
	s.mu.Lock()
	s.instances["tail"] = i
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/api/instances/tail/terminal", &websocket.DialOptions{Subprotocols: []string{"omo", "omo-token." + s.token}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	close(p.exited)
	select {
	case <-i.writerDone:
	case <-ctx.Done():
		t.Fatal("input writer did not stop")
	}
	close(p.readReady)
	kind, data, err := c.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(data) != "last output\n" {
		t.Fatalf("final output lost: %v %q %v", kind, data, err)
	}
}
