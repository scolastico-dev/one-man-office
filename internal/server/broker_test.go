package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestSlowSubscriberIsDisconnectedNotBlocking(t *testing.T) {
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	slow, err := s.events.Subscribe(-1)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Cancel()
	fast, err := s.events.Subscribe(-1)
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Cancel()
	// A write must never wait for the slow subscriber, which reads nothing
	// here: a write that did would hang for good once its buffer is full.
	// Acknowledge each fast delivery before writing again: scheduling the
	// consumer late must not accidentally turn it into a slow subscriber too.
	var prev int64
	for i := range 300 {
		n := m.create()
		ev, ok := <-fast.Events
		if !ok || ev.Kind != api.KindTicketCreated || ev.Ticket == nil || *ev.Ticket != n || ev.ID <= prev {
			t.Fatalf("event %d: %+v (open %v), want ticket %d after event %d", i, ev, ok, n, prev)
		}
		prev = ev.ID
	}
	waitFanOut(t, s, lastEventID(t, s))

	// The slow subscriber was disconnected: its channel holds at most its
	// buffer and is closed.
	n := 0
	for open := true; open; {
		select {
		case _, ok := <-slow.Events:
			if ok {
				n++
			}
			open = ok
		}
	}
	if n > subscriberBuffer {
		t.Fatalf("slow subscriber received %d events, buffer is %d", n, subscriberBuffer)
	}
	slow.Cancel() // after disconnect: no panic
}

func TestBrokerPollsWithoutWake(t *testing.T) {
	s := newTestServer(t)
	sub, err := s.events.Subscribe(-1)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()
	id, err := db.AppendEvent(s.db, api.KindServerPaused, `{"role":"user"}`, 0, `{"paused":true}`)
	if err != nil {
		t.Fatal(err)
	}
	ev := <-sub.Events
	if ev.ID != id {
		t.Fatalf("id %d, want %d", ev.ID, id)
	}
}

func TestBrokerSubscribeAtSnapshotOfUnstreamedEvent(t *testing.T) {
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	m.create()
	// A snapshot's event_id may name a plugin audit row, the newest of all.
	snapshot, err := db.AppendEvent(s.db, "plugin_manual_requested", "cli", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.events.Subscribe(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()
	if sub.Resync || sub.From != snapshot {
		t.Fatalf("resync %v from %d, want live after %d", sub.Resync, sub.From, snapshot)
	}
	n := m.create()
	ev := <-sub.Events
	if *ev.Ticket != n {
		t.Fatalf("ticket %v, want %d", ev.Ticket, n)
	}
}

func TestBrokerRunStopsAndClosesSubscriptions(t *testing.T) {
	s := newTestServer(t)
	b := newBroker(s.db, nil)
	for range 1000 {
		b.Wake() // never blocks, also without Run
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		b.Run(ctx)
	}()
	sub, err := b.Subscribe(-1)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-stopped
	_, ok := <-sub.Events
	if ok {
		t.Fatal("unexpected event")
	}
	sub.Cancel()
	if _, err := b.Subscribe(-1); !errors.Is(err, errBrokerClosed) {
		t.Fatalf("Subscribe after stop: %v", err)
	}
}

func TestBrokerToAPIEvent(t *testing.T) {
	ev := toAPIEvent(db.Event{ID: 7, Kind: "plugin_x", Actor: "cli", Detail: "not json", CreatedAt: "2026-11-15T10:00:00.123Z"})
	if ev.Actor.Role != "cli" || ev.Ticket != nil || string(ev.Data) != "{}" || ev.At != "2026-11-15T10:00:00.123Z" {
		t.Fatalf("plain row: %+v", ev)
	}
	ev = toAPIEvent(db.Event{ID: 8, Kind: api.KindTicketCreated, TicketID: 3, Actor: `{"role":"developer","session":"s1"}`, Detail: `{"a":1}`})
	if ev.Actor.Role != api.RoleDeveloper || ev.Actor.Session != "s1" || *ev.Ticket != 3 || string(ev.Data) != `{"a":1}` {
		t.Fatalf("board row: %+v", ev)
	}
	data, err := json.Marshal(toAPIEvent(db.Event{ID: 9, Kind: "x.y", Detail: "null"}))
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(data, &back); err != nil || string(back["data"]) != "{}" || string(back["ticket"]) != "null" {
		t.Fatalf("wire form %s", data)
	}
}

func TestBrokerResyncBoundary(t *testing.T) {
	for _, tc := range []struct {
		missing    int
		wantResync bool
	}{
		{maxReplay - 1, false},
		{maxReplay, false},
		{maxReplay + 1, true},
	} {
		s := newTestServer(t)
		since := lastEventID(t, s)
		for range tc.missing {
			if _, err := db.AppendEvent(s.db, api.KindServerPaused, `{"role":"user"}`, 0, `{"paused":true}`); err != nil {
				t.Fatal(err)
			}
		}
		sub, err := s.events.Subscribe(since)
		if err != nil {
			t.Fatal(err)
		}
		if sub.Resync != tc.wantResync {
			t.Fatalf("%d missing: Resync %v, want %v", tc.missing, sub.Resync, tc.wantResync)
		}
		if tc.wantResync {
			if sub.From != lastEventID(t, s) || len(sub.Events) != 0 {
				t.Fatalf("%d missing: from %d, %d queued", tc.missing, sub.From, len(sub.Events))
			}
		} else if sub.From != since || len(sub.Events) != tc.missing {
			t.Fatalf("%d missing: from %d, %d replayed", tc.missing, sub.From, len(sub.Events))
		}
		sub.Cancel()
	}
}

func subscriberCount(b *broker) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

func TestSubscriptionRemovedAfterClientDisconnect(t *testing.T) {
	s := newTestServer(t)
	st := openStream(t, newBrowser(t, s), s, "", nil)
	if n := subscriberCount(s.events); n != 1 {
		t.Fatalf("%d subscribers while connected, want 1", n)
	}
	st.close()
	deadline := time.Now().Add(streamTimeout)
	for subscriberCount(s.events) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscription still registered after disconnect")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBrokerSubscribeRacingClose(t *testing.T) {
	s := newTestServer(t)
	b := newBroker(s.db, nil)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		b.Run(ctx)
	}()

	const workers = 8
	type result struct {
		sub *subscription
		err error
	}
	results := make(chan result, workers*50)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 50 {
				sub, err := b.Subscribe(-1)
				results <- result{sub, err}
			}
		}()
	}
	close(start)
	cancel()
	wg.Wait()
	<-stopped
	close(results)

	for r := range results {
		if r.err != nil {
			if !errors.Is(r.err, errBrokerClosed) || r.sub != nil {
				t.Fatalf("Subscribe: %v, %v", r.sub, r.err)
			}
			continue
		}
		// A subscription handed out before the stop must be closed by it.
		for open := true; open; {
			select {
			case _, ok := <-r.sub.Events:
				open = ok
			}
		}
		r.sub.Cancel()
	}
	if sub, err := b.Subscribe(-1); err == nil || sub != nil {
		t.Fatalf("Subscribe after close: %v, %v", sub, err)
	}
	if n := subscriberCount(b); n != 0 {
		t.Fatalf("%d subscribers left after close", n)
	}
}
