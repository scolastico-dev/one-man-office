package relaytransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// A protocol peer isolates scheduling from encryption, which belongs to RecordIO.
type recordPeer struct {
	in, out chan relayproto.PlainRecord
	done    chan struct{}
	once    sync.Once
}

func (p *recordPeer) Send(ctx context.Context, r relayproto.PlainRecord) error {
	select {
	case p.out <- r:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return net.ErrClosed
	}
}
func (p *recordPeer) Receive(ctx context.Context) (relayproto.PlainRecord, error) {
	select {
	case r := <-p.in:
		return r, nil
	case <-ctx.Done():
		return relayproto.PlainRecord{}, ctx.Err()
	case <-p.done:
		return relayproto.PlainRecord{}, net.ErrClosed
	}
}
func (p *recordPeer) Close() error { p.once.Do(func() { close(p.done) }); return nil }
func newPeer(t *testing.T) (*Mux, *recordPeer) {
	t.Helper()
	p := &recordPeer{in: make(chan relayproto.PlainRecord), out: make(chan relayproto.PlainRecord), done: make(chan struct{})}
	m := New(context.Background(), p, func() error { return nil })
	t.Cleanup(func() { m.Close(); m.Wait() })
	return m, p
}
func record(kind string, id uint64) relayproto.PlainRecord {
	r := relayproto.PlainRecord{V: 1, Type: kind, ID: relayproto.Counter(id)}
	if kind == "stream.reset" {
		r.Code = "cancelled"
	}
	return r
}
func openPeer(t *testing.T, m *Mux, p *recordPeer, id uint64) net.Conn {
	t.Helper()
	p.in <- record("stream.open", id)
	r := <-p.out
	if r.Type != "stream.opened" || uint64(r.ID) != id {
		t.Fatalf("open: %+v", r)
	}
	c, e := m.Accept()
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestStreamBytesCreditHalfClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p := newPeer(t)
		c := openPeer(t, m, p, 1)
		data := record("stream.data", 1)
		data.Data = []byte("request")
		p.in <- data
		synctest.Wait()
		select {
		case r := <-p.out:
			t.Fatalf("credit before consumption: %s", r.Type)
		default:
		}
		b := make([]byte, 7)
		if _, e := io.ReadFull(c, b); e != nil || string(b) != "request" {
			t.Fatal(string(b), e)
		}
		credit := <-p.out
		if credit.Type != "stream.credit" || credit.Bytes != 7 {
			t.Fatal(credit)
		}
		p.in <- record("stream.fin", 1)
		if n, e := c.Read(b); n != 0 || e != io.EOF {
			t.Fatal(n, e)
		}
		written := make(chan error, 1)
		go func() { _, e := c.Write(bytes.Repeat([]byte("x"), 40000)); written <- e }()
		total := 0
		for total < 40000 {
			r := <-p.out
			if r.Type != "stream.data" || len(r.Data) > 16384 {
				t.Fatal(r.Type, len(r.Data))
			}
			total += len(r.Data)
		}
		if e := <-written; e != nil {
			t.Fatal(e)
		}
		if e := c.(interface{ CloseWrite() error }).CloseWrite(); e != nil {
			t.Fatal(e)
		}
		if r := <-p.out; r.Type != "stream.fin" {
			t.Fatal(r.Type)
		}
	})
}
func TestBackpressureDoesNotBlockAnotherStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p := newPeer(t)
		a := openPeer(t, m, p, 1)
		b := openPeer(t, m, p, 3)
		done := make(chan error, 1)
		go func() { _, e := a.Write(bytes.Repeat([]byte("a"), 65537)); done <- e }()
		total := 0
		for total < 65536 {
			r := <-p.out
			total += len(r.Data)
		}
		synctest.Wait()
		select {
		case e := <-done:
			t.Fatalf("write exceeded credit: %v", e)
		default:
		}
		other := make(chan error, 1)
		go func() { _, e := b.Write([]byte("b")); other <- e }()
		r := <-p.out
		if r.ID != 3 || string(r.Data) != "b" {
			t.Fatal(r)
		}
		if e := <-other; e != nil {
			t.Fatal(e)
		}
		cr := record("stream.credit", 1)
		cr.Bytes = 1
		p.in <- cr
		r = <-p.out
		if r.ID != 1 || len(r.Data) != 1 {
			t.Fatal(r)
		}
		if e := <-done; e != nil {
			t.Fatal(e)
		}
		// A stalled receiver has exactly 64 KiB of bounded buffered data too.
		for range 4 {
			r := record("stream.data", 1)
			r.Data = bytes.Repeat([]byte("z"), 16384)
			p.in <- r
		}
		p.in <- record("stream.reset", 1)
		if r := <-p.out; r.Type != "stream.reset" {
			t.Fatal(r.Type)
		}
		awaitReset(a.(*Conn))
		if _, e := a.Read(make([]byte, 1)); e == nil {
			t.Fatal("reset left readable data")
		}
	})
}
func TestProtocolStates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records []relayproto.PlainRecord
	}{
		{"first-id", []relayproto.PlainRecord{record("stream.open", 3)}},
		{"even-id", []relayproto.PlainRecord{record("stream.open", 2)}},
		{"reuse", []relayproto.PlainRecord{record("stream.open", 1), record("stream.open", 1)}},
		{"unknown", []relayproto.PlainRecord{record("stream.fin", 1)}},
		{"host-opened", []relayproto.PlainRecord{record("stream.open", 1), record("stream.opened", 1)}},
		{"double-fin", []relayproto.PlainRecord{record("stream.open", 1), record("stream.fin", 1), record("stream.fin", 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, p := newPeer(t)
			go func() {
				for _, r := range tc.records {
					select {
					case p.in <- r:
					case <-p.done:
						return
					}
				}
			}()
			go func() {
				for {
					select {
					case <-p.out:
					case <-p.done:
						return
					}
				}
			}()
			if e := m.Wait(); !errors.Is(e, relayproto.ErrProtocol) {
				t.Fatal(e)
			}
		})
	}
}
func TestStreamLimitAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p := newPeer(t)
		var first net.Conn
		for i := uint64(1); i <= 127; i += 2 {
			c := openPeer(t, m, p, i)
			if first == nil {
				first = c
			}
		}
		p.in <- record("stream.open", 129)
		r := <-p.out
		if r.Type != "stream.reset" || r.Code != "refused" {
			t.Fatal(r)
		}
		p.in <- record("stream.reset", 1)
		if r := <-p.out; r.Type != "stream.reset" {
			t.Fatal(r.Type)
		}
		awaitReset(first.(*Conn))
		if _, e := first.Read(make([]byte, 1)); e == nil {
			t.Fatal("reset succeeded")
		}
		c := openPeer(t, m, p, 131)
		blocked := make(chan error, 1)
		go func() { _, e := c.Read(make([]byte, 1)); blocked <- e }()
		m.Close()
		if e := <-blocked; e == nil {
			t.Fatal("close did not cancel read")
		}
		if _, e := m.Accept(); e == nil {
			t.Fatal("accept after close")
		}
	})
}
func TestConnectionDeadlines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p := newPeer(t)
		c := openPeer(t, m, p, 1)
		c.SetReadDeadline(time.Now().Add(time.Second))
		_, e := c.Read(make([]byte, 1))
		var ne net.Error
		if !errors.As(e, &ne) || !ne.Timeout() {
			t.Fatal(e)
		}
		c.SetReadDeadline(time.Time{})
		p.in <- record("stream.fin", 1)
		if _, e = c.Read(make([]byte, 1)); e != io.EOF {
			t.Fatal(e)
		}
		c.SetWriteDeadline(time.Now().Add(time.Second))
		_, e = c.Write([]byte("blocked writer"))
		if !errors.As(e, &ne) || !ne.Timeout() {
			t.Fatal(e)
		}
	})
}

func TestPartialCreditUnblocksPendingLargeWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p := newPeer(t)
		c := openPeer(t, m, p, 1)
		done := make(chan error, 1)
		go func() { _, e := c.Write(bytes.Repeat([]byte("x"), 100000)); done <- e }()
		for range 4 {
			<-p.out
		}
		synctest.Wait()
		cr := record("stream.credit", 1)
		cr.Bytes = 1
		p.in <- cr
		synctest.Wait()
		r := <-p.out
		if r.Type != "stream.data" || len(r.Data) != 1 {
			t.Fatal(r.Type, len(r.Data))
		}
		m.Close()
		if e := <-done; e == nil {
			t.Fatal("unfinished write succeeded")
		}
	})
}
func TestResetBeforeAcceptCannotFillListenerQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p := newPeer(t)
		for id := uint64(1); id <= 131; id += 2 {
			select {
			case p.in <- record("stream.open", id):
			case <-p.done:
				t.Fatal("session failed while opening")
			}
			r := <-p.out
			if r.Type != "stream.opened" {
				t.Fatal(r.Type)
			}
			select {
			case p.in <- record("stream.reset", id):
			case <-p.done:
				t.Fatal("session failed before reset")
			}
			if r := <-p.out; r.Type != "stream.reset" {
				t.Fatal(r.Type)
			}
		}
		c := openPeer(t, m, p, 133)
		if c == nil {
			t.Fatal("missing connection")
		}
	})
}
func TestRejectOversendAndCreditOverflow(t *testing.T) {
	for _, kind := range []string{"oversend", "credit", "after-fin", "oversize", "control"} {
		t.Run(kind, func(t *testing.T) {
			m, p := newPeer(t)
			openPeer(t, m, p, 1)
			switch kind {
			case "oversend":
				for range 4 {
					r := record("stream.data", 1)
					r.Data = bytes.Repeat([]byte("x"), 16384)
					p.in <- r
				}
				r := record("stream.data", 1)
				r.Data = []byte("x")
				p.in <- r
			case "credit":
				r := record("stream.credit", 1)
				r.Bytes = 1
				p.in <- r
			case "after-fin":
				p.in <- record("stream.fin", 1)
				r := record("stream.data", 1)
				r.Data = []byte("x")
				p.in <- r
			case "oversize":
				r := record("stream.data", 1)
				r.Data = make([]byte, 16385)
				p.in <- r
			case "control":
				p.in <- relayproto.PlainRecord{V: 1, Type: "ready.ack", PairEpoch: make([]byte, 16)}
			}
			if e := m.Wait(); !errors.Is(e, relayproto.ErrProtocol) {
				t.Fatal(e)
			}
		})
	}
}

func awaitReset(c *Conn) {
	for {
		c.mux.mu.Lock()
		if c.failure != nil {
			c.mux.mu.Unlock()
			return
		}
		ch := c.mux.changed
		c.mux.mu.Unlock()
		<-ch
	}
}

func TestResetCrossingsStayBoundedAndDoNotKillOtherStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p := newPeer(t)
		other := openPeer(t, m, p, 1)
		for id := uint64(3); id < 203; id += 2 {
			c := openPeer(t, m, p, id)
			// Host has sent bytes whose peer credit may still cross a local reset.
			sent := make(chan error, 1)
			go func() { _, e := c.Write([]byte("out")); sent <- e }()
			<-p.out
			if e := <-sent; e != nil {
				t.Fatal(e)
			}
			data := record("stream.data", id)
			data.Data = []byte("unread")
			p.in <- data
			// Wait for the exact buffered input that makes Close cancel the stream.
			cc := c.(*Conn)
			for {
				m.mu.Lock()
				ready := len(cc.input) == 6
				ch := m.changed
				m.mu.Unlock()
				if ready {
					break
				}
				<-ch
			}
			c.Close()
			r := <-p.out
			if r.Type != "stream.reset" {
				t.Fatal(r.Type)
			}
			data.Data = []byte("crossed")
			p.in <- data
			credit := record("stream.credit", id)
			credit.Bytes = 3
			p.in <- credit
			p.in <- record("stream.fin", id)
			ack := record("stream.reset", id)
			ack.Code = "closed"
			p.in <- ack
			// Opening the next stream is also a receive-order barrier for the ack.
		}
		openPeer(t, m, p, 203) // ordered acknowledgment barrier
		done := make(chan error, 1)
		go func() { _, e := other.Write([]byte("alive")); done <- e }()
		r := <-p.out
		if r.ID != 1 || string(r.Data) != "alive" {
			t.Fatal(r)
		}
		if e := <-done; e != nil {
			t.Fatal(e)
		}
		m.mu.Lock()
		count := len(m.streams)
		m.mu.Unlock()
		if count != 2 {
			t.Fatal("reset state retained", count)
		}
	})
}
func TestRemoteResetIsAcknowledgedAndRetiresAfterWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p := newPeer(t)
		c := openPeer(t, m, p, 1)
		p.in <- record("stream.reset", 1)
		r := <-p.out
		if r.Type != "stream.reset" || r.Code != "closed" {
			t.Fatal(r)
		}
		awaitReset(c.(*Conn))
		if _, e := c.Write([]byte("x")); e == nil {
			t.Fatal("reset write succeeded")
		}
		openPeer(t, m, p, 3)
	})
}

func TestResetAdmissionBoundAndSafeReclamation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p := newPeer(t)
		other := openPeer(t, m, p, 1)
		for id := uint64(3); id <= 127; id += 2 {
			c := openPeer(t, m, p, id)
			r := record("stream.data", id)
			r.Data = []byte("unread")
			p.in <- r
			cc := c.(*Conn)
			for {
				m.mu.Lock()
				ready := len(cc.input) > 0
				ch := m.changed
				m.mu.Unlock()
				if ready {
					break
				}
				<-ch
			}
			c.Close()
			if r := <-p.out; r.Type != "stream.reset" {
				t.Fatal(r.Type)
			}
		}
		p.in <- record("stream.open", 129)
		if r := <-p.out; r.Type != "stream.reset" || r.Code != "refused" {
			t.Fatal(r)
		}
		m.mu.Lock()
		slots := len(m.streams)
		m.mu.Unlock()
		if slots != 64 {
			t.Fatal(slots)
		}
		done := make(chan error, 1)
		go func() { _, e := other.Write([]byte("alive")); done <- e }()
		if r := <-p.out; string(r.Data) != "alive" {
			t.Fatal(r)
		}
		if e := <-done; e != nil {
			t.Fatal(e)
		}
		// Late traffic is still valid for the oldest resetting stream; no eviction.
		r := record("stream.data", 3)
		r.Data = []byte("late")
		p.in <- r
		for id := uint64(3); id <= 127; id += 2 {
			p.in <- record("stream.reset", id)
		}
		openPeer(t, m, p, 131)
	})
}

func TestWriterServicesRefusalsDuringContinuousStreamTraffic(t *testing.T) {
	m := &Mux{streams: map[relayproto.Counter]*Conn{}}
	c := &Conn{id: 1, mux: m, opened: true, credit: 1, recvCredit: 65535}
	m.order = []*Conn{c}
	m.streams[1] = c
	m.refused = []relayproto.PlainRecord{{V: 1, Type: "stream.reset", ID: 3, Code: "refused"}}
	for range 65 {
		item := m.nextLocked()
		if item.record.Type == "stream.reset" {
			return
		}
		// The application consumes the next byte while another is being written;
		// this stream remains continuously ready without growing a queue.
		c.credit = 1
		c.recvCredit = 65535
	}
	t.Fatal("control refusal starved behind a continuously ready stream")
}

func TestStreamRequiresAdmissionAuthority(t *testing.T) {
	p := &recordPeer{in: make(chan relayproto.PlainRecord), out: make(chan relayproto.PlainRecord), done: make(chan struct{})}
	m := New(context.Background(), p, nil)
	defer m.Close()
	p.in <- record("stream.open", 1)
	if e := m.Wait(); !errors.Is(e, relayproto.ErrAuthentication) {
		t.Fatal(e)
	}
}

type failingRecordIO struct {
	*recordPeer
	count int
}

func (f *failingRecordIO) Send(context.Context, relayproto.PlainRecord) error {
	f.count++
	return io.ErrUnexpectedEOF
}
func TestUncertainSendClosesSessionWithoutReplay(t *testing.T) {
	p := &failingRecordIO{recordPeer: &recordPeer{in: make(chan relayproto.PlainRecord), done: make(chan struct{})}}
	m := New(context.Background(), p, func() error { return nil })
	defer m.Close()
	p.in <- record("stream.open", 1)
	if e := m.Wait(); !errors.Is(e, io.ErrUnexpectedEOF) {
		t.Fatal(e)
	}
	if p.count != 1 {
		t.Fatal("uncertain write replayed", p.count)
	}
	if _, e := m.Accept(); e == nil {
		t.Fatal("failed open admitted")
	}
}
