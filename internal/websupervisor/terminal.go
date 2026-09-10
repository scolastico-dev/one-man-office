package websupervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

func (s *Server) terminal(w http.ResponseWriter, r *http.Request) {
	i := s.instance(w, r)
	if i == nil {
		return
	}
	select {
	case s.connections <- struct{}{}:
		defer func() { <-s.connections }()
	default:
		http.Error(w, "too many terminal connections", 429)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"omo"}})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(terminalInputLimit)
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	initial, stream, detach := i.subscribe()
	defer detach()
	written := make(chan error, 8)
	acknowledge := func(err error) {
		select {
		case written <- err:
		case <-ctx.Done():
		}
	}
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
					Rows uint16 `json:"rows"`
					Cols uint16 `json:"cols"`
				}
				if json.Unmarshal(data, &size) != nil || i.resize(size.Rows, size.Cols) != nil {
					return
				}
			}
		}
	}()
	write := func(kind websocket.MessageType, data []byte) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return c.Write(ctx, kind, data)
	}
	if len(initial) > 0 {
		if err := write(websocket.MessageBinary, initial); err != nil {
			return
		}
	}
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
		case err := <-written:
			if err != nil {
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
