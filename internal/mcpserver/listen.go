package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A listener is the open notification stream of an agent session: a GET of
// /mcp that accepts text/event-stream, the standalone stream the Streamable
// HTTP transport gives a server for notifications it sends on its own. An open
// stream does not prove that the agent program shows notifications to the
// model, so omo sends a probe over each new stream: a notification carrying a
// one-time token that the agent returns with `omo ai lsp confirm TOKEN` (the
// tool lsp_confirm) from its own session. A session counts as listening only
// while a stream is open and its probe was confirmed; a new stream (reconnect,
// omo restart, bridge reopen) needs a new confirmation. A session that merely
// used the endpoint, listed its tools or advertised a capability is not
// listening.
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
	// changed is closed, and replaced, whenever a stream becomes confirmed
	// or goes away; Wait sleeps on it.
	changed chan struct{}
}

// NewListeners returns an empty registry.
func NewListeners() *Listeners {
	return &Listeners{by: map[string]map[uint64]*listener{}, changed: make(chan struct{})}
}

type listener struct {
	mu    sync.Mutex // serializes writes; guards ready and dead
	w     http.ResponseWriter
	rc    *http.ResponseController
	ready bool   // the opening line was written
	dead  bool   // a write failed
	token string // the probe's one-time token
	// confirmed is set when the agent returned token.
	confirmed bool
	end       chan struct{}
}

// Confirmed reports whether session has an established, working stream.
func (l *Listeners) Confirmed(session string) bool {
	for _, ls := range l.of(session) {
		ls.mu.Lock()
		ok := ls.ready && ls.confirmed && !ls.dead
		ls.mu.Unlock()
		if ok {
			return true
		}
	}
	return false
}

// Confirm marks the stream whose probe carried token as confirmed, for the
// session that returned it. It reports whether it did: a token of another
// session's stream, a stale token of a stream that is gone and a wrong token
// confirm nothing.
func (l *Listeners) Confirm(session, token string) bool {
	if token == "" {
		return false
	}
	ok := false
	for _, ls := range l.of(session) {
		ls.mu.Lock()
		if ls.ready && !ls.dead && subtle.ConstantTimeCompare([]byte(ls.token), []byte(token)) == 1 {
			ls.confirmed, ok = true, true
		}
		ls.mu.Unlock()
	}
	if ok {
		l.mu.Lock()
		l.signalLocked()
		l.mu.Unlock()
	}
	return ok
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
	l.signalLocked()
}

// signalLocked wakes every Wait; the caller holds l.mu.
func (l *Listeners) signalLocked() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// Wait returns once Confirmed(session) equals want, or ctx ends. It waits
// for the change itself, so it needs no polling interval.
func (l *Listeners) Wait(ctx context.Context, session string, want bool) error {
	for {
		l.mu.Lock()
		changed := l.changed
		l.mu.Unlock()
		if l.Confirmed(session) == want {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// write sends one frame; false when the stream is not (or no longer) ready
// or the write failed, in which case the stream ends.
func (ls *listener) write(frame string) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if !ls.ready || !ls.confirmed || ls.dead {
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
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "no randomness", http.StatusInternalServerError)
		return
	}
	ls := &listener{w: w, rc: http.NewResponseController(w), end: make(chan struct{}), token: hex.EncodeToString(raw[:])}
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
	if ready {
		if probe, err := notificationFrame(probeText(ls.token)); err == nil {
			ready = ls.send(probe)
		}
	}
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

// probeText is the notification that asks the agent to confirm that it sees
// notifications from omo.
func probeText(token string) string {
	return "omo checks that you receive notifications from omo over MCP. Confirm it now by running `omo ai lsp confirm " + token +
		"` (MCP tool lsp_confirm). Until you do, omo tells you about language-server issues in the terminal instead."
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
