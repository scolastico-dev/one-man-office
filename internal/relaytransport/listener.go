package relaytransport

import "net"

type address string

func (a address) Network() string { return "omo-relay" }
func (a address) String() string  { return string(a) }
func (m *Mux) Addr() net.Addr     { return address("omo-relay.invalid") }
func (m *Mux) Accept() (net.Conn, error) {
	for {
		m.mu.Lock()
		if m.err != nil {
			m.mu.Unlock()
			return nil, net.ErrClosed
		}
		for _, c := range m.order {
			if c.acceptReady && !c.accepted && c.failure == nil {
				c.accepted = true
				m.mu.Unlock()
				return c, nil
			}
		}
		ch := m.changed
		m.mu.Unlock()
		<-ch
	}
}
