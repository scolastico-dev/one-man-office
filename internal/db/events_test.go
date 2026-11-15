package db

import "testing"

func TestEventsSinceStreamedOnly(t *testing.T) {
	d := open(t)
	if _, err := AppendEvent(d, "plugin_manual_requested", "caller", 0, "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendEvent(d, "ticket.created", "{}", 0, "{}"); err != nil {
		t.Fatal(err)
	}
	got, err := EventsSince(d, 0, 0, true)
	if err != nil || len(got) != 1 || got[0].Kind != "ticket.created" {
		t.Fatalf("streamed events = %+v, %v", got, err)
	}
	all, err := EventsSince(d, 0, 0, false)
	if err != nil || len(all) != 2 {
		t.Fatalf("all events = %+v, %v", all, err)
	}
	limited, err := EventsSince(d, all[0].ID, 1, false)
	if err != nil || len(limited) != 1 || limited[0].ID != all[1].ID {
		t.Fatalf("limited events = %+v, %v", limited, err)
	}
	if last, err := LastEventID(d); err != nil || last != all[1].ID {
		t.Fatalf("LastEventID = %d, %v", last, err)
	}
}

func TestAppendEventZeroTicketStoresNull(t *testing.T) {
	d := open(t)
	id, err := AppendEvent(d, "plugin_error", "", 0, "boom")
	if err != nil || id == 0 {
		t.Fatalf("AppendEvent = %d, %v", id, err)
	}
	var isNull int
	if err := d.QueryRow(`SELECT ticket_id IS NULL FROM events WHERE id=?`, id).Scan(&isNull); err != nil || isNull != 1 {
		t.Fatalf("ticket_id null = %d, %v", isNull, err)
	}
}
