package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/relayhost"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
	"github.com/scolastico-dev/one-man-office/internal/relayserver"
)

// Start with already locally accepted durable devices: the real host manager
// reconnects from private state, without any UI WebSocket or test SetCallbacks.
func notificationServer(t *testing.T) (*Server, *httptest.Server, []relayhost.Device) {
	t.Helper()
	rs, err := relayserver.New(relayserver.Config{})
	if err != nil {
		t.Fatal(err)
	}
	relay := httptest.NewServer(rs)
	t.Cleanup(relay.Close)
	t.Cleanup(func() { rs.Close() })
	layout := testHome(t)
	store, err := relayhost.OpenStore(layout.Relay)
	if err != nil {
		t.Fatal(err)
	}
	host, _ := relayproto.GenerateIdentity()
	hostFP, _ := relayproto.Fingerprint("host", host.PublicKey())
	devices := make([]relayhost.Device, 2)
	for i := range devices {
		id, _ := relayproto.GenerateIdentity()
		ui, _ := relayproto.Fingerprint("ui", id.PublicKey())
		d := relayhost.Device{UIID: ui, PublicKey: id.PublicKey(), PairEpoch: make([]byte, 16), NotificationRoot: make([]byte, 32), AID: make([]byte, 16), AcceptedAt: time.Now().UTC(), State: relayhost.Active, Generation: uint64(i + 1)}
		rand.Read(d.PairEpoch)
		rand.Read(d.NotificationRoot)
		rand.Read(d.AID)
		devices[i] = d
	}
	err = store.Update(func(v *relayhost.State) error {
		v.Seed = host.Seed()
		v.HostFP = hostFP
		v.Origin = relay.URL
		v.HostID = "office"
		v.Devices = devices
		v.NextGeneration = 2
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.RelayURL = relay.URL
	cfg.RelayEnabled = true
	s := openServer(t, layout, cfg)
	waitFor(t, "relay registration", func() bool { return s.relay.Status().State == "connected" })
	return s, relay, devices
}
func notificationPoll(t *testing.T, s *Server, d relayhost.Device, ack *relayproto.Receipt) (relayproto.NotificationContext, relayproto.Poll, []byte) {
	t.Helper()
	status := s.relay.Status()
	nc := relayproto.NotificationContext{Route: relayproto.Route{Origin: status.Origin, HostID: status.HostID}, HostFP: status.HostFP, UIID: d.UIID, PairEpoch: d.PairEpoch}
	request := make([]byte, 32)
	rand.Read(request)
	p, err := relayproto.NewPoll(nc, d.NotificationRoot, request, ack)
	if err != nil {
		t.Fatal(err)
	}
	return nc, p, request
}
func notificationGET(ctx context.Context, relayURL, ui string, p relayproto.Poll) (int, []byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", relayURL+"/notifications/office/"+ui, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Omo-Notification-Request", p.RequestHeader)
	req.Header.Set("Omo-Notification-MAC", p.RequestMAC)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, resp.Header, err
}
func decryptedPoll(t *testing.T, s *Server, relayURL string, d relayhost.Device, ack *relayproto.Receipt) (relayproto.NotificationPage, relayproto.NotificationEnvelope) {
	t.Helper()
	nc, p, request := notificationPoll(t, s, d, ack)
	code, raw, header, err := notificationGET(t.Context(), relayURL, d.UIID, p)
	if err != nil || code != 200 || header.Get("Cache-Control") != "no-store" || header.Get("Content-Type") != relayproto.NotificationContentType {
		t.Fatalf("GET: %d %v %v", code, header, err)
	}
	env, err := relayproto.DecodeNotificationEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	pg, err := relayproto.OpenNotification(nc, d.NotificationRoot, request, env)
	if err != nil {
		t.Fatal(err)
	}
	return pg, env
}
func notificationReceipt(p relayproto.NotificationPage) *relayproto.Receipt {
	return &relayproto.Receipt{Batch: p.Batch, Through: p.Through, Token: p.Token}
}

func TestRelayNotificationsProductionPollingWithoutUIWebSocket(t *testing.T) {
	s, relay, devices := notificationServer(t)
	pm, n, first := pmMentionsUser(t, s, "first")
	for range 51 {
		if _, err := s.board.Comment(t.Context(), board.Actor{Role: api.RolePM, Session: pm.ID, Ticket: int64(n)}, int64(n), api.CommentRequest{Body: "@user next"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.board.SetMentionsSeen(t.Context(), board.User, first+51); err != nil {
		t.Fatal(err)
	}
	// Both independent devices can poll concurrently and receive their own receipts.
	var wg sync.WaitGroup
	pages := make([]relayproto.NotificationPage, 2)
	envs := make([]relayproto.NotificationEnvelope, 2)
	for i := range devices {
		wg.Go(func() { pages[i], envs[i] = decryptedPoll(t, s, relay.URL, devices[i], nil) })
	}
	wg.Wait()
	for i, p := range pages {
		if len(p.Notifications) != 50 || p.Notifications[0].ID != relayproto.Counter(first) || !p.More || s.relay.LiveConnections(devices[i].UIID) != 0 {
			t.Fatal("no-WS independent page", i, p)
		}
	}
	if bytes.Equal(pages[0].Batch, pages[1].Batch) || bytes.Equal(pages[0].Token, pages[1].Token) {
		t.Fatal("devices share receipts")
	}
	// Address, epoch, request and ciphertext authentication prevent swapped/forged replies.
	nc, _, request := notificationPoll(t, s, devices[0], nil)
	if _, err := relayproto.OpenNotification(nc, devices[0].NotificationRoot, request, envs[1]); err == nil {
		t.Fatal("cross-device reply accepted")
	}
	tampered := envs[0]
	tampered.Ciphertext = bytes.Clone(tampered.Ciphertext)
	tampered.Ciphertext[0] ^= 1
	if _, err := relayproto.OpenNotification(nc, devices[0].NotificationRoot, envs[0].Request, tampered); err == nil {
		t.Fatal("tampering accepted")
	}
	// Retry re-reads changed/deleted source content without changing receipt IDs.
	if _, err := s.db.Exec(`UPDATE comments SET body = ? WHERE id = ?`, "fresh preview", first+1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM comments WHERE id = ?`, first); err != nil {
		t.Fatal(err)
	}
	retry, _ := decryptedPoll(t, s, relay.URL, devices[0], nil)
	if !notificationReceipt(retry).Equal(*notificationReceipt(pages[0])) || !retry.Notifications[0].Unavailable || retry.Notifications[1].Body != "fresh preview" {
		t.Fatal("retry source refresh")
	}
	bad, _ := decryptedPoll(t, s, relay.URL, devices[1], notificationReceipt(pages[0]))
	if bad.Error != "invalid_ack" {
		t.Fatal("cross-device receipt accepted")
	}
	y, _ := decryptedPoll(t, s, relay.URL, devices[0], notificationReceipt(pages[0]))
	if len(y.Notifications) != 2 || !bytes.Equal(y.Acknowledged, pages[0].Batch) {
		t.Fatal("ack page", y)
	}
	duplicate, _ := decryptedPoll(t, s, relay.URL, devices[0], notificationReceipt(pages[0]))
	if !reflect.DeepEqual(y, duplicate) {
		t.Fatal("duplicate ack consumed newer page")
	}
	empty, _ := decryptedPoll(t, s, relay.URL, devices[0], notificationReceipt(y))
	if len(empty.Notifications) != 0 || empty.Notifications == nil || empty.Through != relayproto.Counter(first+51) || !bytes.Equal(empty.Acknowledged, y.Batch) {
		t.Fatal("authenticated empty", empty)
	}
	browser, err := s.board.UserMentions(t.Context(), board.User, 50)
	if err != nil || browser.SeenThrough != first+51 || browser.Unread != 0 {
		t.Fatal("browser seen changed", browser, err)
	}
}

func TestRelayNotificationsUntrustedGETNeverConsumes(t *testing.T) {
	s, relay, devices := notificationServer(t)
	_, _, id := pmMentionsUser(t, s, "private")
	d := devices[0]
	nc, p, request := notificationPoll(t, s, d, nil)
	bad := p
	bad.RequestMAC = base64.RawURLEncoding.EncodeToString(make([]byte, 48))
	// A sender with public identity information but without the private pairing
	// root can produce well-formed crypto, but neither polls nor replies authenticate.
	publicOnly := make([]byte, 32)
	forged, err := relayproto.NewPoll(nc, publicOnly, request, nil)
	if err != nil {
		t.Fatal(err)
	}
	forgedEnv, err := relayproto.SealNotification(nc, publicOnly, request, relayproto.NotificationPage{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = relayproto.OpenNotification(nc, d.NotificationRoot, request, forgedEnv); err == nil {
		t.Fatal("public-only reply forgery")
	}
	for _, poll := range []relayproto.Poll{bad, forged} {
		code, raw, _, err := notificationGET(t.Context(), relay.URL, d.UIID, poll)
		if err != nil || code != 204 || len(raw) != 0 {
			t.Fatal("invalid authentication", code, err)
		}
	}
	unknown, _ := relayproto.GenerateIdentity()
	unknownUI, _ := relayproto.Fingerprint("ui", unknown.PublicKey())
	code, raw, _, err := notificationGET(t.Context(), relay.URL, unknownUI, p)
	if err != nil || code != 204 || len(raw) != 0 {
		t.Fatal("unknown device", code, err)
	}
	valid, _ := decryptedPoll(t, s, relay.URL, d, nil)
	if len(valid.Notifications) != 1 || valid.Notifications[0].ID != relayproto.Counter(id) {
		t.Fatal("untrusted GET consumed")
	}
	if err = s.relay.Unregister(d.UIID); err != nil {
		t.Fatal(err)
	}
	code, raw, _, err = notificationGET(t.Context(), relay.URL, d.UIID, p)
	if err != nil || code != 204 || len(raw) != 0 {
		t.Fatal("revoked device", code, err)
	}
	other, _ := decryptedPoll(t, s, relay.URL, devices[1], nil)
	if len(other.Notifications) != 1 {
		t.Fatal("revocation affected other device")
	}
	if err = s.relay.Disconnect(); err != nil {
		t.Fatal(err)
	}
	code, raw, _, err = notificationGET(t.Context(), relay.URL, devices[1].UIID, p)
	if err != nil || code != 204 || len(raw) != 0 {
		t.Fatal("offline empty", code, err)
	}
}
