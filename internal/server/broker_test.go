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
	fast, err := s.events.Subscribe(-1)
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Cancel()
	got := make(chan []int64, 1)
	go func() {
		var ids []int64
		for ev := range fast.Events {
			if ev.Kind == api.KindTicketCreated {
				ids = append(ids, ev.ID)
				if len(ids) == 300 {
					break
				}
			}
		}
		got <- ids
	}()

	// A write must never wait for the slow subscriber, which reads nothing
	// here: a write that did would hang for good once its buffer is full.
	// The bound only tells that from a slow disk, so it is generous; a loaded
	// machine takes more than 100 ms for a single write now and then.
	for i := range 300 {
		start := time.Now()
		m.create()
		if d := time.Since(start); d > streamTimeout {
			t.Fatalf("write %d took %v", i, d)
		}
	}
	waitFanOut(t, s, lastEventID(t, s))

	// The slow subscriber was disconnected: its channel holds at most its
	// buffer and is closed.
	n := 0
	deadline := time.After(streamTimeout)
	for open := true; open; {
		select {
		case _, ok := <-slow.Events:
			if ok {
				n++
			}
			open = ok
		case <-deadline:
			t.Fatal("slow subscriber was not disconnected")
		}
	}
	if n > subscriberBuffer {
		t.Fatalf("slow subscriber received %d events, buffer is %d", n, subscriberBuffer)
	}
	slow.Cancel() // after disconnect: no panic

	// The fast subscriber got every event, in order.
	select {
	case ids := <-got:
		for i := 1; i < len(ids); i++ {
			if ids[i] <= ids[i-1] {
				t.Fatalf("ids not increasing at %d: %v", i, ids[i-1:i+1])
			}
		}
		if len(ids) != 300 {
			t.Fatalf("fast subscriber got %d events", len(ids))
		}
	case <-time.After(streamTimeout):
		t.Fatal("fast subscriber did not get all events")
	}
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
	select {
	case ev := <-sub.Events:
		if ev.ID != id {
			t.Fatalf("id %d, want %d", ev.ID, id)
		}
	case <-time.After(streamTimeout):
		t.Fatal("event not delivered without a wake-up")
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
	select {
	case ev := <-sub.Events:
		if *ev.Ticket != n {
			t.Fatalf("ticket %v, want %d", ev.Ticket, n)
		}
	case <-time.After(streamTimeout):
		t.Fatal("no event")
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
	select {
	case <-stopped:
	case <-time.After(streamTimeout):
		t.Fatal("Run did not return")
	}
	select {
	case _, ok := <-sub.Events:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(streamTimeout):
		t.Fatal("subscription not closed")
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
		deadline := time.After(streamTimeout)
		for open := true; open; {
			select {
			case _, ok := <-r.sub.Events:
				open = ok
			case <-deadline:
				t.Fatal("subscription survived the broker stopping")
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
