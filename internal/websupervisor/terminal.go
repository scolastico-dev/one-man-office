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
	c.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	initial, stream, detach := i.subscribe()
	defer detach()
	go func() {
		defer cancel()
		for {
			kind, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			switch kind {
			case websocket.MessageBinary:
				if err := i.input(data); err != nil {
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
	write := func(data []byte) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return c.Write(ctx, websocket.MessageBinary, data)
	}
	if len(initial) > 0 {
		if err := write(initial); err != nil {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-stream:
			if !ok {
				_ = c.Close(websocket.StatusNormalClosure, "terminal stream ended")
				return
			}
			if err := write(data); err != nil {
				return
			}
		}
	}
}
