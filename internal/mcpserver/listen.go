package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A listener is the open notification stream of an agent session: a GET of
// /mcp that accepts text/event-stream, the standalone stream the Streamable
// HTTP transport gives a server for notifications it sends on its own. omo
// treats a session as listening only while such a stream is open and the
// opening line has been written to it; a session that merely used the
// endpoint, listed its tools or advertised a capability is not listening.
//
// What a listener receives is a JSON-RPC notifications/message (the MCP
// logging notification) with the logger "omo", the level "error" (so that no
// level an agent program filters at hides it) and the text in data.

// keepAlive is the pause between two comment lines that keep the stream open
// through proxies and find a client that went away without closing it.
const keepAlive = 15 * time.Second

// writeTimeout bounds one write to a stream; a client that does not read is
// not listening.
const writeTimeout = 5 * time.Second

// Listeners tracks the open notification streams of agent sessions.
type Listeners struct {
	mu   sync.Mutex
	next uint64
	by   map[string]map[uint64]*listener
}

// NewListeners returns an empty registry.
func NewListeners() *Listeners {
	return &Listeners{by: map[string]map[uint64]*listener{}}
}

type listener struct {
	mu    sync.Mutex // serializes writes; guards ready and dead
	w     http.ResponseWriter
	rc    *http.ResponseController
	ready bool // the opening line was written
	dead  bool // a write failed
	end   chan struct{}
}

// Confirmed reports whether session has an established, working stream.
func (l *Listeners) Confirmed(session string) bool {
	for _, ls := range l.of(session) {
		ls.mu.Lock()
		ok := ls.ready && !ls.dead
		ls.mu.Unlock()
		if ok {
			return true
		}
	}
	return false
}

// Notify sends text to the streams of session and reports whether a
// confirmed stream took it. A stream whose write fails is dropped, so the
// caller falls back to another way of telling the agent.
func (l *Listeners) Notify(session, text string) bool {
	frame, err := notificationFrame(text)
	if err != nil {
		return false
	}
	for _, ls := range l.of(session) {
		if ls.write(frame) {
			return true
		}
	}
	return false
}

func (l *Listeners) of(session string) []*listener {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]*listener, 0, len(l.by[session]))
	for _, ls := range l.by[session] {
		out = append(out, ls)
	}
	return out
}

func (l *Listeners) add(session string, ls *listener) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	if l.by[session] == nil {
		l.by[session] = map[uint64]*listener{}
	}
	l.by[session][l.next] = ls
	return l.next
}

func (l *Listeners) remove(session string, id uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.by[session], id)
	if len(l.by[session]) == 0 {
		delete(l.by, session)
	}
}

// write sends one frame; false when the stream is not (or no longer) ready
// or the write failed, in which case the stream ends.
func (ls *listener) write(frame string) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if !ls.ready || ls.dead {
		return false
	}
	return ls.send(frame)
}

// send writes and flushes frame; the caller holds ls.mu.
func (ls *listener) send(frame string) bool {
	_ = ls.rc.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := fmt.Fprint(ls.w, frame)
	if err == nil {
		err = ls.rc.Flush()
	}
	_ = ls.rc.SetWriteDeadline(time.Time{})
	if err != nil {
		ls.dead = true
		select {
		case <-ls.end:
		default:
			close(ls.end)
		}
		return false
	}
	return true
}

// serve holds the notification stream of session open until the client goes
// away or a write fails.
func (l *Listeners) serve(w http.ResponseWriter, r *http.Request, session string) {
	ls := &listener{w: w, rc: http.NewResponseController(w), end: make(chan struct{})}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	// Registered first, written second: a notification sent meanwhile waits
	// for the opening line, and finds the stream unusable if it fails.
	id := l.add(session, ls)
	defer l.remove(session, id)
	ls.mu.Lock()
	w.WriteHeader(http.StatusOK)
	ls.ready = ls.send(": omo notification stream\n\n")
	ready := ls.ready
	ls.mu.Unlock()
	if !ready {
		return
	}
	tick := time.NewTicker(keepAlive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			ls.mu.Lock()
			ls.dead = true
			ls.mu.Unlock()
			return
		case <-ls.end:
			return
		case <-tick.C:
			ls.mu.Lock()
			if !ls.dead {
				ls.send(": ping\n\n")
			}
			ls.mu.Unlock()
		}
	}
}

// notificationFrame is the SSE event that carries text to the agent.
func notificationFrame(text string) (string, error) {
	data, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/message",
		"params":  map[string]any{"level": "error", "logger": "omo", "data": text},
	})
	if err != nil {
		return "", err
	}
	return "event: message\ndata: " + string(data) + "\n\n", nil
}

// wantsStream reports whether r asks for a notification stream.
func wantsStream(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}
