package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

const (
	// subscriberBuffer is how many live events a subscriber may fall
	// behind before it is disconnected.
	subscriberBuffer = 256
	// maxReplay is how many missed events a reconnecting subscriber gets
	// from the database; with more missing it must resync.
	maxReplay = 500
	// pollInterval bounds how long a committed event waits for fan-out
	// when no wake-up arrives (for example for writes outside the board).
	pollInterval = time.Second
	// fanOutBatch is how many events one database read fetches.
	fanOutBatch = 1000
)

// errBrokerClosed is returned by Subscribe once the broker has stopped.
var errBrokerClosed = errors.New("event broker stopped")

// broker tails the event log and fans committed events out to subscribers.
//
// The log is the source of truth: Wake only says "something may have been
// committed", and the broker then reads every streamed event after the last
// one it delivered. Because SQLite commits writes one at a time and event ids
// grow with every insert, reading in id order after a cursor never skips an
// event that commits later. Fan-out never blocks: a subscriber whose buffer
// is full is disconnected (its channel closed) and resumes by id.
type broker struct {
	db   *sql.DB
	log  *log.Logger
	wake chan struct{}

	// pingInterval is how often the stream handler sends a keep-alive
	// comment; set before any stream starts.
	pingInterval time.Duration

	mu     sync.Mutex
	ready  bool  // cursor is initialised
	cursor int64 // every streamed event up to this id has been fanned out
	closed bool
	subs   map[*subscription]struct{}
}

// subscription is one subscriber's view of the stream.
type subscription struct {
	// Events delivers events with ids greater than From, in id order. It is
	// closed when the subscriber falls behind or the broker stops.
	Events <-chan api.Event
	// From is the id the stream continues after.
	From int64
	// Resync is set when the requested resume point could not be served
	// gap-free; the stream continues after From instead.
	Resync bool

	ch     chan api.Event
	after  int64 // last id queued to ch
	b      *broker
	closed bool
}

func newBroker(d *sql.DB, logger *log.Logger) *broker {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &broker{
		db:           d,
		log:          logger,
		wake:         make(chan struct{}, 1),
		pingInterval: 20 * time.Second,
		subs:         make(map[*subscription]struct{}),
	}
}

// Wake signals that events may have been committed. It never blocks and may
// be called from any goroutine, also while other locks are held.
func (b *broker) Wake() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// Run fans out committed events until ctx ends, on every wake-up and at
// least once per poll interval. When it returns, every subscription is
// closed and Subscribe fails.
func (b *broker) Run(ctx context.Context) {
	defer b.stop()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.wake:
		case <-tick.C:
		}
		b.mu.Lock()
		err := b.advance()
		b.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			b.log.Printf("event stream: read events: %v", err)
		}
	}
}

func (b *broker) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for s := range b.subs {
		b.drop(s)
	}
}

// advance fans out every streamed event committed after the cursor. The
// caller holds b.mu.
func (b *broker) advance() error {
	if !b.ready {
		last, err := db.LastEventID(b.db)
		if err != nil {
			return err
		}
		b.cursor, b.ready = last, true
	}
	for {
		evs, err := db.EventsSince(b.db, b.cursor, fanOutBatch, true)
		if err != nil {
			return err
		}
		for _, e := range evs {
			ev := toAPIEvent(e)
			for s := range b.subs {
				if ev.ID <= s.after {
					continue
				}
				select {
				case s.ch <- ev:
					s.after = ev.ID
				default:
					b.drop(s)
				}
			}
			b.cursor = e.ID
		}
		if len(evs) < fanOutBatch {
			return nil
		}
	}
}

// drop disconnects s. The caller holds b.mu.
func (b *broker) drop(s *subscription) {
	if s.closed {
		return
	}
	s.closed = true
	delete(b.subs, s)
	close(s.ch)
}

// Subscribe starts a subscription that delivers every streamed event after
// the id after, or after the newest event when after is negative. Up to
// maxReplay missed events are replayed from the database first; when more
// are missing, or after names an id the log does not have yet, the
// subscription has Resync set and continues after the newest event.
func (b *broker) Subscribe(after int64) (*subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, errBrokerClosed
	}
	// Catch up first, so the cursor is the newest committed event and
	// nothing between the replay and the live events is missed.
	if err := b.advance(); err != nil {
		return nil, err
	}
	s := &subscription{b: b, From: b.cursor, after: b.cursor}
	var replay []api.Event
	switch {
	case after < 0 || after == b.cursor:
	case after > b.cursor:
		// The newest ids may belong to events that are not streamed (a
		// snapshot's event_id is the newest id of all). Only an id the
		// log has never reached needs a resync.
		last, err := db.LastEventID(b.db)
		if err != nil {
			return nil, err
		}
		if after > last {
			s.Resync = true
		} else {
			s.From, s.after = after, after
		}
	default:
		evs, err := db.EventsSince(b.db, after, maxReplay+1, true)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			if e.ID <= b.cursor {
				replay = append(replay, toAPIEvent(e))
			}
		}
		if len(replay) > maxReplay {
			s.Resync, replay = true, nil
		} else {
			s.From = after
		}
	}
	s.ch = make(chan api.Event, len(replay)+subscriberBuffer)
	s.Events = s.ch
	for _, e := range replay {
		s.ch <- e
	}
	b.subs[s] = struct{}{}
	return s, nil
}

// Cancel ends the subscription; it is safe to call more than once.
func (s *subscription) Cancel() {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.drop(s)
}

// sent returns the id of the last event fanned out.
func (b *broker) sent() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cursor
}

// toAPIEvent converts a log row to its wire form. Actors stored as JSON are
// decoded; a plain string (plugin audit rows) becomes the role. A detail that
// is not a JSON object becomes {}.
func toAPIEvent(e db.Event) api.Event {
	out := api.Event{ID: e.ID, Kind: e.Kind, At: e.CreatedAt, Data: json.RawMessage(`{}`)}
	if e.TicketID != 0 {
		n := int(e.TicketID)
		out.Ticket = &n
	}
	if json.Unmarshal([]byte(e.Actor), &out.Actor) != nil {
		out.Actor = api.Actor{Role: api.Role(e.Actor)}
	}
	if d := bytes.TrimSpace([]byte(e.Detail)); len(d) > 0 && d[0] == '{' && json.Valid(d) {
		out.Data = json.RawMessage(d)
	}
	return out
}
