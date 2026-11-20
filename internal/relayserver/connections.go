package relayserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

const writerQueueSize = 16

type socket interface {
	Write(context.Context, websocket.MessageType, []byte) error
	Ping(context.Context) error
	CloseNow() error
}

type peer struct {
	conn   socket
	ctx    context.Context
	cancel context.CancelFunc
	out    chan []byte
	// Routing fields below are protected by Server.mu, including dead.
	dead                     bool
	boundAt                  time.Time
	role, hostID             string
	registration, connection relayproto.Bytes
	host                     *peer
	uis                      map[string]*peer
	pending                  map[string]*notification
}

func newPeer(c socket) *peer {
	ctx, cancel := context.WithCancel(context.Background())
	return &peer{conn: c, ctx: ctx, cancel: cancel, out: make(chan []byte, writerQueueSize)}
}

func randomHandle() []byte { b := make([]byte, 16); _, _ = rand.Read(b); return b }

func (s *Server) bind(p *peer, m relayproto.Control) string {
	s.mu.Lock()
	var victims []*peer
	defer func() { s.mu.Unlock(); closePeers(victims) }()
	if p.dead || s.closed {
		return "host_offline"
	}
	p.boundAt = time.Now()
	switch m.Type {
	case "host.register":
		if s.hosts[m.HostID] != nil {
			return "id_in_use"
		}
		p.role, p.hostID, p.registration = "host", m.HostID, randomHandle()
		p.uis = make(map[string]*peer)
		p.pending = make(map[string]*notification)
		s.hosts[m.HostID] = p
		victims = append(victims, s.enqueueLocked(p, relayproto.Control{V: 1, Type: "host.registered", HostID: m.HostID, Registration: p.registration})...)
	case "ui.attach":
		h := s.hosts[m.HostID]
		if h == nil {
			return "host_offline"
		}
		p.role, p.hostID, p.host, p.connection = "ui", m.HostID, h, randomHandle()
		h.uis[string(p.connection)] = p
		victims = append(victims, s.enqueueLocked(p, relayproto.Control{V: 1, Type: "ui.attached", Connection: p.connection})...)
		victims = append(victims, s.enqueueLocked(h, relayproto.Control{V: 1, Type: "ui.open", Connection: p.connection})...)
	default:
		return "protocol_error"
	}
	return ""
}

func (s *Server) route(p *peer, m relayproto.Control) bool {
	s.mu.Lock()
	var victims []*peer
	defer func() { s.mu.Unlock(); closePeers(victims) }()
	if p.dead {
		return false
	}
	if p.role == "ui" {
		if m.Type != "forward" || !bytes.Equal(m.Connection, p.connection) || p.host.dead || p.host.uis[string(p.connection)] != p {
			return false
		}
		victims = append(victims, s.enqueueLocked(p.host, m)...)
		return true
	}
	if p.role != "host" || s.hosts[p.hostID] != p {
		return false
	}
	switch m.Type {
	case "forward", "ui.close":
		u := p.uis[string(m.Connection)]
		// A late response for a departed UI has no authority over another one.
		if u == nil {
			return true
		}
		if m.Type == "ui.close" {
			victims = append(victims, s.dropLocked(u)...)
		} else {
			victims = append(victims, s.enqueueLocked(u, m)...)
		}
	case "notification.reply", "notification.empty":
		s.answerLocked(p, m)
	default:
		return false
	}
	return true
}

// enqueueLocked never blocks. A queue overflow removes precisely the slow
// destination, plus its dependent UIs if that destination is a host.
func (s *Server) enqueueLocked(p *peer, m relayproto.Control) []*peer {
	if p.dead {
		return nil
	}
	b, err := relayproto.EncodeControl(m)
	if err != nil {
		return s.dropLocked(p)
	}
	select {
	case p.out <- b:
		return nil
	default:
		return s.dropLocked(p)
	}
}

func (s *Server) disconnect(p *peer) {
	s.mu.Lock()
	victims := s.dropLocked(p)
	s.mu.Unlock()
	closePeers(victims)
}

func (s *Server) dropLocked(p *peer) []*peer {
	if p.dead {
		return nil
	}
	p.dead = true
	p.cancel()
	delete(s.sockets, p)
	victims := []*peer{p}
	if p.role == "host" {
		if s.hosts[p.hostID] == p {
			delete(s.hosts, p.hostID)
		}
		for _, u := range p.uis {
			victims = append(victims, s.dropLocked(u)...)
		}
		for qid, n := range p.pending {
			delete(p.pending, qid)
			n.reply <- nil
		}
	} else if p.host != nil && !p.host.dead && p.host.uis[string(p.connection)] == p {
		delete(p.host.uis, string(p.connection))
		victims = append(victims, s.enqueueLocked(p.host, relayproto.Control{V: 1, Type: "ui.closed", Connection: p.connection})...)
	}
	return victims
}

func closePeers(peers []*peer) {
	for _, p := range peers {
		_ = p.conn.CloseNow()
	}
}

func (p *peer) writeLoop(s *Server) {
	defer s.disconnect(p)
	for {
		select {
		case <-p.ctx.Done():
			return
		case b := <-p.out:
			ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
			err := p.conn.Write(ctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (p *peer) keepalive(s *Server) {
	next := p.boundAt.Add(20 * time.Second)
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()
	defer s.disconnect(p)
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-timer.C:
			ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
			err := p.conn.Ping(ctx) // Conn.Ping requires its own matching Pong.
			cancel()
			if err != nil {
				return
			}
			next = next.Add(20 * time.Second)
			timer.Reset(time.Until(next))
		}
	}
}
