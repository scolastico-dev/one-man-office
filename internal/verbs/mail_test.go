package verbs

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func TestSendInboxReadOverSocket(t *testing.T) {
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, a := range []db.Agent{
		{Name: "ceo-ada", Role: "ceo", Profile: "p"},
		{Name: "pm-alex", Role: "product_manager", Profile: "p"},
	} {
		db.InsertAgent(d, a)
		db.SetAgentState(d, a.Name, "working")
	}
	mail := &bus.Store{DB: d, Dir: bus.DBDirectory{DB: d}}
	sock := filepath.Join(dir, "omo.sock")
	srv := sockd.New(sock, nil)
	RegisterMail(srv, mail)
	go srv.ListenAndServe()
	defer srv.Close()
	waitSocket(t, sock)

	if err := sockc.Call(sock, "pm-alex", "send",
		proto.SendArgs{To: "ceo-ada", Subject: "report", Body: "spec done", Priority: "normal"}, nil); err != nil {
		t.Fatal(err)
	}
	var inbox []bus.Message
	if err := sockc.Call(sock, "ceo-ada", "inbox", nil, &inbox); err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 1 || inbox[0].Subject != "report" {
		t.Fatalf("inbox = %+v", inbox)
	}
	var msg bus.Message
	if err := sockc.Call(sock, "ceo-ada", "read", proto.ReadArgs{ID: inbox[0].ID}, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Body != "spec done" {
		t.Fatalf("msg = %+v", msg)
	}
	// Routing enforced through the socket too.
	if err := sockc.Call(sock, "pm-alex", "send",
		proto.SendArgs{To: "user", Subject: "x", Body: "y", Priority: "normal"}, nil); err == nil {
		t.Fatal("pm→user must be rejected")
	}
}

func waitSocket(t *testing.T, sock string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := sockc.Call(sock, "probe", "inbox", nil, new([]bus.Message)); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
