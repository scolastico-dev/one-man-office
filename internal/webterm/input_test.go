package webterm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
	p := &pasteTerminal{gate: make(chan struct{}), started: make(chan struct{}, 1), closed: make(chan struct{})}
	i := Own("paste", p, nil)
	server := serveTerminal(t, i)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint.Scheme = "ws"
	endpoint.Path = "/terminal"
	c, _, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{Subprotocols: []string{"omo"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	expectReplayEnd(t, ctx, c)
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
	p := terminalFixture()
	i := Own("tail", p, nil)
	server := serveTerminal(t, i)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/terminal", &websocket.DialOptions{Subprotocols: []string{"omo"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	expectReplayEnd(t, ctx, c)
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

func TestTerminalReconnectDeliversPrefixBeforeReplayAndRepaintsOnce(t *testing.T) {
	p := terminalFixture()
	i := Own("reconnect", p, nil)
	i.publish([]byte("\x1b[?1049h\x1b[?1002h\x1b[?1006h\x1b[?2004h"))
	server := serveTerminal(t, i)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/terminal", &websocket.DialOptions{Subprotocols: []string{"omo"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	kind, prefix, err := c.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(prefix) != "\x1b[?1049h\x1b[?2004h\x1b[?1002h\x1b[?1006h" {
		t.Fatalf("initial prefix = %q (%v, %v)", prefix, kind, err)
	}
	kind, replay, err := c.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(replay) != "\x1b[?1049h\x1b[?1002h\x1b[?1006h\x1b[?2004h" {
		t.Fatalf("initial replay = %q (%v, %v)", replay, kind, err)
	}
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"rows":40,"cols":120}`)); err != nil {
		t.Fatal(err)
	}
	if got := waitForResizeCalls(p, 2); len(got) < 2 || got[0] != [2]uint16{40, 119} || got[1] != [2]uint16{40, 120} {
		t.Fatalf("repaint resize calls = %v", got)
	}
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"rows":41,"cols":121}`)); err != nil {
		t.Fatal(err)
	}
	if got := waitForResizeCalls(p, 3); len(got) != 3 || got[2] != [2]uint16{41, 121} {
		t.Fatalf("ordinary resize calls = %v", got)
	}
}

func TestTerminalPlainShellDoesNotRepaintOnFirstResize(t *testing.T) {
	p := terminalFixture()
	i := Own("shell", p, nil)
	server := serveTerminal(t, i)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/terminal", &websocket.DialOptions{Subprotocols: []string{"omo"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"rows":40,"cols":120}`)); err != nil {
		t.Fatal(err)
	}
	if got := waitForResizeCalls(p, 1); len(got) != 1 || got[0] != [2]uint16{40, 120} {
		t.Fatalf("plain shell resize calls = %v", got)
	}
}

func waitForResizeCalls(p *controlledTerminal, want int) [][2]uint16 {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.resizeMu.Lock()
		got := append([][2]uint16(nil), p.resizes...)
		p.resizeMu.Unlock()
		if len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.resizeMu.Lock()
	defer p.resizeMu.Unlock()
	return append([][2]uint16(nil), p.resizes...)
}

// serveTerminal exposes the terminal's websocket at /terminal.
func serveTerminal(t *testing.T, term *Terminal) *httptest.Server {
	t.Helper()
	limit := NewConnectionLimit(16)
	mux := http.NewServeMux()
	mux.HandleFunc("/terminal", func(w http.ResponseWriter, r *http.Request) {
		term.ServeWebSocket(r.Context(), w, r, limit)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// replayEnd is the frame that follows the replayed history on every attach.
const replayEnd = `{"type":"replay-end"}`

func TestTerminalAttachMarksEndOfReplay(t *testing.T) {
	p := terminalFixture()
	i := Own("replay", p, nil)
	// A query the program wrote earlier: the browser's emulator answers it
	// while replaying, and that answer must not reach the program again.
	i.publish([]byte("before\x1b[c"))
	server := serveTerminal(t, i)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dial := func() *websocket.Conn {
		c, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/terminal", &websocket.DialOptions{Subprotocols: []string{"omo"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.CloseNow() })
		return c
	}
	c := dial()
	kind, data, err := c.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(data) != "before\x1b[c" {
		t.Fatalf("replay = %v %q %v", kind, data, err)
	}
	kind, data, err = c.Read(ctx)
	if err != nil || kind != websocket.MessageText || string(data) != replayEnd {
		t.Fatalf("after the replay = %v %q %v", kind, data, err)
	}
	i.publish([]byte("live"))
	kind, data, err = c.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(data) != "live" {
		t.Fatalf("live output = %v %q %v", kind, data, err)
	}

	// An attach with nothing to replay is marked as well.
	empty := Own("empty", terminalFixture(), nil)
	server = serveTerminal(t, empty)
	c = dial()
	kind, data, err = c.Read(ctx)
	if err != nil || kind != websocket.MessageText || string(data) != replayEnd {
		t.Fatalf("empty attach = %v %q %v", kind, data, err)
	}
}

// expectReplayEnd reads the frame that ends the (empty) replay of an attach.
func expectReplayEnd(t *testing.T, ctx context.Context, c *websocket.Conn) {
	t.Helper()
	kind, data, err := c.Read(ctx)
	if err != nil || kind != websocket.MessageText || string(data) != replayEnd {
		t.Fatalf("end of replay = %v %q %v", kind, data, err)
	}
}
