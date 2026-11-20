package relayhost

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

type noticeSource struct {
	notices []relayproto.Notification
	err     error
	before  func(context.Context)
	reads   int
}

func (s *noticeSource) RelayMentionsAfter(ctx context.Context, after relayproto.Counter, limit, maxBytes int) (relayproto.NotificationPage, error) {
	s.reads++
	if s.before != nil {
		s.before(ctx)
	}
	if s.err != nil {
		return relayproto.NotificationPage{}, s.err
	}
	pg := relayproto.NotificationPage{Through: after, Notifications: []relayproto.Notification{}}
	for _, n := range s.notices {
		if n.ID > after {
			if len(pg.Notifications) == limit {
				pg.More = true
				break
			}
			pg.Notifications = append(pg.Notifications, n)
			pg.Through = n.ID
		}
	}
	return pg, nil
}
func (s *noticeSource) RelayMentionsByID(ctx context.Context, ids []relayproto.Counter) (relayproto.NotificationPage, error) {
	s.reads++
	if s.before != nil {
		s.before(ctx)
	}
	if s.err != nil {
		return relayproto.NotificationPage{}, s.err
	}
	pg := relayproto.NotificationPage{Through: ids[len(ids)-1], Notifications: []relayproto.Notification{}}
	for _, id := range ids {
		n := relayproto.Notification{ID: id, Unavailable: true}
		for _, v := range s.notices {
			if v.ID == id {
				n = v
			}
		}
		pg.Notifications = append(pg.Notifications, n)
	}
	for _, n := range s.notices {
		if n.ID > pg.Through {
			pg.More = true
		}
	}
	return pg, nil
}
func notificationDevice(t *testing.T, m *Manager, s *Store) Device {
	t.Helper()
	id, _ := relayproto.GenerateIdentity()
	ui, _ := relayproto.Fingerprint("ui", id.PublicKey())
	d := Device{UIID: ui, PublicKey: id.PublicKey(), PairEpoch: bytes.Repeat([]byte{1}, 16), NotificationRoot: bytes.Repeat([]byte{2}, 32), AID: bytes.Repeat([]byte{3}, 16), AcceptedAt: time.Now().UTC(), State: Active}
	if err := s.Update(func(v *State) error {
		v.NextGeneration++
		d.Generation = v.NextGeneration
		v.Devices = append(v.Devices, d)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return d
}
func notificationSource(count int) *noticeSource {
	s := &noticeSource{}
	for i := 1; i <= count; i++ {
		s.notices = append(s.notices, relayproto.Notification{ID: relayproto.Counter(i), Ticket: 1, AuthorRole: "pm", CreatedAt: "2026-11-20T00:00:00Z", Body: "@user hello", Title: "Ticket"})
	}
	return s
}
func pollDevice(t *testing.T, m *Manager, d Device, source NotificationSource, ack *relayproto.Receipt) (relayproto.NotificationPage, relayproto.NotificationEnvelope, error) {
	t.Helper()
	v, _ := m.store.Snapshot()
	nc := relayproto.NotificationContext{Route: relayproto.Route{Origin: v.Origin, HostID: v.HostID}, HostFP: v.HostFP, UIID: d.UIID, PairEpoch: d.PairEpoch}
	request := bytes.Repeat([]byte{42}, 32)
	poll, err := relayproto.NewPoll(nc, d.NotificationRoot, request, ack)
	if err != nil {
		t.Fatal(err)
	}
	var env relayproto.NotificationEnvelope
	err = m.WithDevice(context.Background(), d.UIID, func(ctx context.Context, latest Device) error {
		var e error
		env, e = m.PollNotifications(ctx, latest, poll, source)
		return e
	})
	if err != nil {
		return relayproto.NotificationPage{}, env, err
	}
	pg, err := relayproto.OpenNotification(nc, d.NotificationRoot, request, env)
	return pg, env, err
}
func receiptOf(pg relayproto.NotificationPage) *relayproto.Receipt {
	return &relayproto.Receipt{Batch: pg.Batch, Through: pg.Through, Token: pg.Token}
}

// Losing an ack reply must preserve the newer pending batch even across restart.
func TestNotificationsReceiptsRestartAndOutOfOrder(t *testing.T) {
	m, s := managerForTest(t)
	if err := m.Connect(ConnectOptions{Origin: "https://relay.example", ClientID: "office"}); err != nil {
		t.Fatal(err)
	}
	d := notificationDevice(t, m, s)
	source := notificationSource(101)
	x, e1, err := pollDevice(t, m, d, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Notifications) != 50 || x.Through != 50 || !x.More || len(x.Acknowledged) != 0 {
		t.Fatal("first page", x)
	}
	retry, e2, err := pollDevice(t, m, d, source, nil)
	if err != nil || !reflect.DeepEqual(x, retry) || bytes.Equal(e1.Reply, e2.Reply) || bytes.Equal(e1.Ciphertext, e2.Ciphertext) {
		t.Fatal("retry changed receipt or reused key", err)
	}
	v, _ := s.Snapshot()
	if v.Devices[0].Cursor != 0 || v.Devices[0].LastAck != nil || !v.Devices[0].Pending.Receipt.Equal(*receiptOf(x)) {
		t.Fatal("GET consumed or unpersisted pending")
	}
	y, _, err := pollDevice(t, m, d, source, receiptOf(x))
	if err != nil || y.Through != 100 || !bytes.Equal(y.Acknowledged, x.Batch) {
		t.Fatal("ack x", err)
	}
	m.Close()
	s2, err := OpenStore(filepath.Clean(s.dir))
	if err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(s2, Options{Origin: "https://relay.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	again, _, err := pollDevice(t, m2, d, source, receiptOf(x))
	if err != nil || !reflect.DeepEqual(y, again) {
		t.Fatal("lost reply recovery changed Y", err)
	}
	forged := *receiptOf(x)
	forged.Token = bytes.Repeat([]byte{99}, 32)
	before, _ := s2.Snapshot()
	bad, _, err := pollDevice(t, m2, d, source, &forged)
	after, _ := s2.Snapshot()
	if err != nil || bad.Error != "invalid_ack" || !reflect.DeepEqual(before, after) {
		t.Fatal("low-through forged ack mutated state", err)
	}
	z, _, err := pollDevice(t, m2, d, source, receiptOf(y))
	if err != nil || z.Through != 101 || z.More {
		t.Fatal("ack y", err)
	}
	before, _ = s2.Snapshot()
	bad, _, err = pollDevice(t, m2, d, source, receiptOf(x))
	after, _ = s2.Snapshot()
	if err != nil || bad.Error != "invalid_ack" || !reflect.DeepEqual(before, after) {
		t.Fatal("old ack accepted", err)
	}
	empty, _, err := pollDevice(t, m2, d, source, receiptOf(z))
	if err != nil || len(empty.Notifications) != 0 || empty.Through != 101 || !bytes.Equal(empty.Acknowledged, z.Batch) {
		t.Fatal("online empty", err)
	}
}

func TestNotificationsCrashAfterAckAndSourceRefresh(t *testing.T) {
	m, s := managerForTest(t)
	if err := m.Connect(ConnectOptions{Origin: "https://relay.example"}); err != nil {
		t.Fatal(err)
	}
	d := notificationDevice(t, m, s)
	source := notificationSource(51)
	x, _, err := pollDevice(t, m, d, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	source.notices = source.notices[1:]
	source.notices[0].Body = "changed preview"
	refresh, _, err := pollDevice(t, m, d, source, nil)
	if err != nil || !refresh.Notifications[0].Unavailable || refresh.Notifications[1].Body != "changed preview" || !receiptOf(refresh).Equal(*receiptOf(x)) {
		t.Fatal("refresh", err)
	}
	source.err = errors.New("source read interrupted after ack commit")
	if _, _, err = pollDevice(t, m, d, source, receiptOf(x)); err == nil {
		t.Fatal("expected source interruption")
	}
	state, _ := s.Snapshot()
	saved := state.Devices[0]
	if saved.Cursor != 50 || saved.Pending != nil || !saved.LastAck.Equal(*receiptOf(x)) {
		t.Fatal("ack not atomically durable")
	}
	m.Close()
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	next, err := NewManager(reopened, Options{Origin: state.Origin, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	source.err = nil
	y, _, err := pollDevice(t, next, d, source, receiptOf(x))
	if err != nil || y.Through != 51 || !bytes.Equal(y.Acknowledged, x.Batch) {
		t.Fatal("crash recovery", err)
	}
}

func TestNotificationsConcurrentPollsKeepOneReceipt(t *testing.T) {
	m, s := managerForTest(t)
	if err := m.Connect(ConnectOptions{Origin: "https://relay.example"}); err != nil {
		t.Fatal(err)
	}
	d := notificationDevice(t, m, s)
	source := notificationSource(51)
	x, _, err := pollDevice(t, m, d, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	pages := make(chan relayproto.NotificationPage, 12)
	for range 12 {
		wg.Go(func() {
			pg, _, err := pollDevice(t, m, d, source, receiptOf(x))
			if err != nil {
				t.Error(err)
				return
			}
			pages <- pg
		})
	}
	wg.Wait()
	close(pages)
	var first *relayproto.Receipt
	for pg := range pages {
		if pg.Through != 51 || !bytes.Equal(pg.Acknowledged, x.Batch) {
			t.Fatal("concurrent acknowledgment consumed newer batch")
		}
		if first == nil {
			first = receiptOf(pg)
		} else if !first.Equal(*receiptOf(pg)) {
			t.Fatal("concurrent receipt replacement")
		}
	}
}

func TestNotificationsInvalidAuthenticationHasNoSourceOrStateEffects(t *testing.T) {
	m, s := managerForTest(t)
	if err := m.Connect(ConnectOptions{Origin: "https://relay.example"}); err != nil {
		t.Fatal(err)
	}
	d := notificationDevice(t, m, s)
	source := notificationSource(1)
	x, _, err := pollDevice(t, m, d, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := s.Snapshot()
	nc := relayproto.NotificationContext{Route: relayproto.Route{Origin: state.Origin, HostID: state.HostID}, HostFP: state.HostFP, UIID: d.UIID, PairEpoch: d.PairEpoch}
	for _, test := range []string{"MAC", "epoch", "root"} {
		t.Run(test, func(t *testing.T) {
			c := nc.Clone()
			root := d.NotificationRoot
			if test == "epoch" {
				c.PairEpoch = bytes.Repeat([]byte{87}, 16)
			}
			if test == "root" {
				root = bytes.Repeat([]byte{88}, 32)
			}
			poll, err := relayproto.NewPoll(c, root, bytes.Repeat([]byte{89}, 32), receiptOf(x))
			if err != nil {
				t.Fatal(err)
			}
			if test == "MAC" {
				poll.RequestMAC = base64Key(bytes.Repeat([]byte{90}, 48))
			}
			reads := source.reads
			before, _ := s.Snapshot()
			err = m.WithDevice(context.Background(), d.UIID, func(ctx context.Context, latest Device) error {
				_, e := m.PollNotifications(ctx, latest, poll, source)
				return e
			})
			after, _ := s.Snapshot()
			if err == nil || source.reads != reads || !reflect.DeepEqual(before, after) {
				t.Fatal("unauthenticated poll read/acknowledged source", err)
			}
		})
	}
}

// Block precisely in the source read, then cancel or revoke. Neither path may
// persist the prepared batch, emit a reply, or return before the callback joins.
func TestNotificationsConsumerCancellationAndRevocation(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "revoke"}[revoke], func(t *testing.T) {
			entered := make(chan struct{})
			exited := make(chan struct{})
			source := notificationSource(1)
			source.before = func(ctx context.Context) { close(entered); <-ctx.Done(); close(exited) }
			var f *hostFixture
			f = newHostFixture(t, Callbacks{ServeSession: func(ctx context.Context, _ Capability, _ relayproto.RecordIO) error { <-ctx.Done(); return nil }, PollNotifications: func(ctx context.Context, d Device, p relayproto.Poll) (relayproto.NotificationEnvelope, error) {
				return f.m.PollNotifications(ctx, d, p, source)
			}})
			d, _, session := f.pair(bytes.Repeat([]byte{61}, 16), false)
			defer session.Close()
			v, _ := f.store.Snapshot()
			nc := relayproto.NotificationContext{Route: relayproto.Route{Origin: v.Origin, HostID: v.HostID}, HostFP: v.HostFP, UIID: d.UIID, PairEpoch: d.PairEpoch}
			p, err := relayproto.NewPoll(nc, d.NotificationRoot, bytes.Repeat([]byte{62}, 32), nil)
			if err != nil {
				t.Fatal(err)
			}
			qid := bytes.Repeat([]byte{63}, 16)
			f.write(relayproto.Control{V: 1, Type: "notification.get", HostID: v.HostID, UIID: d.UIID, QID: qid, RequestHeader: p.RequestHeader, RequestMAC: p.RequestMAC})
			<-entered
			f.m.mu.Lock()
			work := f.m.current.polls[base64Key(qid)]
			f.m.mu.Unlock()
			if revoke {
				if err = f.m.Unregister(d.UIID); err != nil {
					t.Fatal(err)
				}
			} else {
				f.write(relayproto.Control{V: 1, Type: "notification.cancel", QID: qid})
			}
			<-work.done
			<-exited
			state, err := f.store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if revoke {
				if len(state.Devices) != 0 {
					t.Fatal("revoked device retained")
				}
				closed := f.read()
				if closed.Type != "ui.close" || !bytes.Equal(closed.Connection, bytes.Repeat([]byte{61}, 16)) {
					t.Fatal("revocation did not close its UI endpoint", closed.Type)
				}
			} else if state.Devices[0].Pending != nil || state.Devices[0].Cursor != 0 {
				t.Fatal("cancelled read committed receipt")
			}
			// A following unknown-device poll is a control-reader barrier. An earlier
			// reply would arrive first and fail this assertion.
			f.write(relayproto.Control{V: 1, Type: "notification.get", HostID: v.HostID, UIID: d.UIID, QID: bytes.Repeat([]byte{64}, 16), RequestHeader: p.RequestHeader, RequestMAC: base64Key(bytes.Repeat([]byte{0}, 48))})
			reply := f.read()
			if reply.Type != "notification.empty" || !bytes.Equal(reply.QID, bytes.Repeat([]byte{64}, 16)) {
				t.Fatal("cancelled request emitted reply", reply.Type)
			}
		})
	}
}
