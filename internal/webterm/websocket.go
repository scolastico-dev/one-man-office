package webterm

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// replayEndFrame follows the replayed history on every attach, also when
// there was nothing to replay.
const replayEndFrame = `{"type":"replay-end"}`

// ConnectionLimit bounds concurrent websocket connections.
type ConnectionLimit struct{ slots chan struct{} }

// NewConnectionLimit allows at most n concurrent connections.
func NewConnectionLimit(n int) *ConnectionLimit {
	return &ConnectionLimit{slots: make(chan struct{}, n)}
}

// ServeWebSocket attaches the request's websocket to the terminal until either
// side ends. A full limit is answered with 429.
func (i *Terminal) ServeWebSocket(parent context.Context, w http.ResponseWriter, r *http.Request, limit *ConnectionLimit) {
	if limit != nil {
		select {
		case limit.slots <- struct{}{}:
			defer func() { <-limit.slots }()
		default:
			http.Error(w, "too many terminal connections", http.StatusTooManyRequests)
			return
		}
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"omo"}})
	if err != nil {
		return
	}
	defer c.CloseNow()
	// A message frame carries up to terminalInputLimit bytes of text as JSON.
	c.SetReadLimit(4 * terminalInputLimit)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	initial, stream, detach := i.subscribe()
	defer detach()
	// What the writer reports: the write of a binary frame (id 0) or of a message.
	type result struct {
		err     error
		message int64
	}
	written := make(chan result, 8)
	acknowledge := func(err error) {
		select {
		case written <- result{err: err}:
		case <-ctx.Done():
		}
	}
	acknowledgeMessage := func(id int64) func(error) {
		return func(err error) {
			select {
			case written <- result{err: err, message: id}:
			case <-ctx.Done():
			}
		}
	}
	// A message that cannot be written is answered, and the connection stays.
	refuseMessage := func(id int64, err error) {
		select {
		case written <- result{err: err, message: id}:
		case <-ctx.Done():
		}
	}
	write := func(kind websocket.MessageType, data []byte) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return c.Write(ctx, kind, data)
	}
	if len(initial.prefix) > 0 {
		if err := write(websocket.MessageBinary, initial.prefix); err != nil {
			return
		}
	}
	if len(initial.replay) > 0 {
		if err := write(websocket.MessageBinary, initial.replay); err != nil {
			return
		}
	}
	// The browser's emulator answers queries in the replayed history (device
	// attributes, cursor position) as if they were new; the marker lets it
	// drop what it generates until here instead of typing it into the process.
	if err := write(websocket.MessageText, []byte(replayEndFrame)); err != nil {
		return
	}
	repaintOnResize := initial.repaintOnResize
	go func() {
		defer cancel()
		for {
			kind, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			switch kind {
			case websocket.MessageBinary:
				if err := i.queueInput(data, acknowledge); err != nil {
					return
				}
			case websocket.MessageText:
				var size struct {
					Type  string   `json:"type"`
					ID    int64    `json:"id"`
					Text  string   `json:"text"`
					Parts []string `json:"parts"`
					Keys  string   `json:"keys"`
					Rows  uint16   `json:"rows"`
					Cols  uint16   `json:"cols"`
				}
				if json.Unmarshal(data, &size) != nil {
					return
				}
				if size.Type == "message" {
					if size.ID <= 0 {
						continue // nobody could be answered; the message is dropped
					}
					parts := size.Parts
					if parts == nil {
						parts = []string{size.Text}
					}
					if err := i.queueMessageParts(parts, size.Keys, acknowledgeMessage(size.ID)); err != nil {
						refuseMessage(size.ID, err)
					}
					continue
				}
				if !validTerminalSize(size.Rows, size.Cols) {
					return
				}
				if repaintOnResize {
					repaintOnResize = false
					if err := i.repaint(size.Rows, size.Cols); err != nil {
						return
					}
				} else if err := i.resize(size.Rows, size.Cols); err != nil {
					return
				}
			}
		}
	}()
	writerDone := i.writerDone
	for {
		select {
		case <-ctx.Done():
			return
		case <-writerDone:
			select {
			case <-i.stopInput:
				// Normal exit still needs to drain final PTY output.
				writerDone = nil
			default:
				return
			}
		case res := <-written:
			if res.message != 0 {
				ack := struct {
					Type  string `json:"type"`
					ID    int64  `json:"id"`
					Error string `json:"error,omitempty"`
				}{Type: "message-ack", ID: res.message}
				if res.err != nil {
					ack.Error = res.err.Error()
				}
				frame, _ := json.Marshal(ack)
				if err := write(websocket.MessageText, frame); err != nil {
					return
				}
				continue
			}
			if res.err != nil {
				return
			}
			if err := write(websocket.MessageText, []byte(`{"type":"input-ack"}`)); err != nil {
				return
			}
		case data, ok := <-stream:
			if !ok {
				_ = c.Close(websocket.StatusNormalClosure, "terminal stream ended")
				return
			}
			if err := write(websocket.MessageBinary, data); err != nil {
				return
			}
		}
	}
}
