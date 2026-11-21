package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// streamWriter is a response writer whose body is read through a pipe, so a
// test reads exactly what the server wrote and waits for it.
type streamWriter struct {
	header http.Header
	pw     *io.PipeWriter
	status chan int
}

func newStreamWriter() (*streamWriter, *bufio.Reader) {
	pr, pw := io.Pipe()
	return &streamWriter{header: http.Header{}, pw: pw, status: make(chan int, 1)}, bufio.NewReader(pr)
}

func (w *streamWriter) Header() http.Header { return w.header }
func (w *streamWriter) WriteHeader(code int) {
	select {
	case w.status <- code:
	default:
	}
}
func (w *streamWriter) Write(p []byte) (int, error) { return w.pw.Write(p) }
func (w *streamWriter) Flush()                      {}

// openListener serves a listening request of session on l and returns the
// reader of its stream, a function that ends the request as the client
// would, and a channel closed when the server finished serving it.
func openListener(t *testing.T, l *Listeners, session string) (*bufio.Reader, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(ctx)
	r.Header.Set("Accept", "text/event-stream")
	w, rd := newStreamWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.serve(w, r, session)
		w.pw.Close()
	}()
	t.Cleanup(func() { cancel(); <-done })
	return rd, cancel, done
}

// readEvent returns the data of the next SSE event, skipping comments.
func readEvent(t *testing.T, rd *bufio.Reader) string {
	t.Helper()
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended: %v", err)
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			return strings.TrimSpace(data)
		}
	}
}

// readReady consumes the stream's opening line.
func readReady(t *testing.T, rd *bufio.Reader) {
	t.Helper()
	line, err := rd.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, ":") {
		t.Fatalf("opening line %q %v", line, err)
	}
	if blank, _ := rd.ReadString('\n'); strings.TrimSpace(blank) != "" {
		t.Fatalf("opening comment is not an event: %q", blank)
	}
}

func TestListenerConfirmedOnlyOnceTheStreamIsEstablished(t *testing.T) {
	l := NewListeners()
	if l.Confirmed("s1") || l.Notify("s1", "x") {
		t.Fatal("a session that never listened is confirmed")
	}
	rd, _, _ := openListener(t, l, "s1")
	readReady(t, rd)
	if err := l.Wait(t.Context(), "s1", true); err != nil || !l.Confirmed("s1") {
		t.Fatal("an established stream is not confirmed")
	}
	if l.Confirmed("s2") {
		t.Fatal("another session is confirmed")
	}
}

func TestListenerDeliversNotificationToItsOwnSessionOnly(t *testing.T) {
	l := NewListeners()
	one, _, _ := openListener(t, l, "s1")
	two, _, _ := openListener(t, l, "s2")
	readReady(t, one)
	readReady(t, two)

	got := make(chan string, 1)
	go func() { got <- readEvent(t, one) }()
	if !l.Notify("s1", "issue A") {
		t.Fatal("not delivered to a confirmed listener")
	}
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Level  string `json:"level"`
			Logger string `json:"logger"`
			Data   string `json:"data"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(<-got), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Method != "notifications/message" || msg.Params.Data != "issue A" || msg.Params.Logger != "omo" || msg.Params.Level != "error" {
		t.Fatalf("notification %+v", msg)
	}

	// session two got nothing: its next event is the one sent now.
	go func() { got <- readEvent(t, two) }()
	if !l.Notify("s2", "issue B") {
		t.Fatal("s2 not delivered")
	}
	if err := json.Unmarshal([]byte(<-got), &msg); err != nil || msg.Params.Data != "issue B" {
		t.Fatalf("s2 received %+v %v", msg, err)
	}
}

func TestListenerDisconnectEndsConfirmation(t *testing.T) {
	l := NewListeners()
	rd, cancel, done := openListener(t, l, "s1")
	readReady(t, rd)
	cancel()
	<-done
	if l.Confirmed("s1") || l.Notify("s1", "x") {
		t.Fatal("a disconnected listener still counts")
	}
}

func TestListenerWriteFailureIsNotDelivery(t *testing.T) {
	l := NewListeners()
	rd, _, done := openListener(t, l, "s1")
	readReady(t, rd)
	// the client side of the pipe breaks without the request ending.
	l.mu.Lock()
	var ls *listener
	for _, m := range l.by["s1"] {
		ls = m
	}
	l.mu.Unlock()
	ls.w.(*streamWriter).pw.CloseWithError(io.ErrClosedPipe)
	if l.Notify("s1", "x") {
		t.Fatal("a failed write counted as delivered")
	}
	<-done
	if l.Confirmed("s1") {
		t.Fatal("a listener whose stream broke is still confirmed")
	}
}

func TestListenerForOnlyAgentSessionsAndStreamRequests(t *testing.T) {
	l := NewListeners()
	h := NewHandler(Options{Listeners: l, Version: "test"})
	// a user connection has no session to notify: GET is the SDK's own answer.
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(WithIdentity(req.Context(), noSession)))
	if rec.Code == http.StatusOK {
		t.Fatalf("a user connection opened a listener: %d", rec.Code)
	}
	if l.Confirmed("s1") {
		t.Fatal("confirmed without a listener")
	}
}
