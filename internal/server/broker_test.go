package server

import (
	"context"
	"encoding/json"
	"errors"
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

	for i := range 300 {
		start := time.Now()
		m.create()
		if d := time.Since(start); d > 100*time.Millisecond {
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
