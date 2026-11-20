package relayproto

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func notificationContext(t *testing.T) NotificationContext {
	t.Helper()
	c := authContext(t, "reconnect")
	hf, _ := Fingerprint("host", c.PKH)
	ui, _ := Fingerprint("ui", c.PKU)
	return NotificationContext{Route: c.Route, HostFP: hf, UIID: ui, PairEpoch: c.PairEpoch}
}
func TestNotificationPollAuthentication(t *testing.T) {
	c := notificationContext(t)
	root := bytes.Repeat([]byte{1}, 32)
	request := bytes.Repeat([]byte{2}, 32)
	receipt := &Receipt{Batch: bytes.Repeat([]byte{3}, 16), Through: 99, Token: bytes.Repeat([]byte{4}, 32)}
	for _, ack := range []*Receipt{nil, receipt} {
		p, e := NewPoll(c, root, request, ack)
		if e != nil {
			t.Fatal(e)
		}
		r, e := VerifyPoll(c, root, p)
		if e != nil || !bytes.Equal(r.Request, request) || (r.Ack == nil) != (ack == nil) {
			t.Fatal(r, e)
		}
	}
	p, e := NewPoll(c, root, request, receipt)
	if e != nil {
		t.Fatal(e)
	}
	for _, mutation := range []string{"mac", "route", "epoch", "host", "ui", "root", "ack", "duplicate", "unknown", "version"} {
		t.Run(mutation, func(t *testing.T) {
			cc := c.Clone()
			rr := bytes.Clone(root)
			pp := p
			switch mutation {
			case "mac":
				b, _ := base64.RawURLEncoding.DecodeString(pp.RequestMAC)
				b[0] ^= 1
				pp.RequestMAC = base64.RawURLEncoding.EncodeToString(b)
			case "route":
				cc.Route.HostID = "other"
			case "epoch":
				cc.PairEpoch[0] ^= 1
			case "host":
				cc.HostFP = string(bytes.Repeat([]byte{'0'}, 96))
			case "ui":
				cc.UIID = string(bytes.Repeat([]byte{'0'}, 96))
			case "root":
				rr[0] ^= 1
			case "ack":
				b, _ := base64.RawURLEncoding.DecodeString(pp.RequestHeader)
				b = bytes.Replace(b, []byte(`"99"`), []byte(`"98"`), 1)
				pp.RequestHeader = base64.RawURLEncoding.EncodeToString(b)
			case "duplicate":
				b, _ := base64.RawURLEncoding.DecodeString(pp.RequestHeader)
				b = bytes.Replace(b, []byte(`"through":"99"`), []byte(`"through":"99","through":"99"`), 1)
				pp.RequestHeader = base64.RawURLEncoding.EncodeToString(b)
			case "unknown":
				b, _ := base64.RawURLEncoding.DecodeString(pp.RequestHeader)
				b = append(b[:len(b)-1], []byte(`,"unknown":1}`)...)
				pp.RequestHeader = base64.RawURLEncoding.EncodeToString(b)
			case "version":
				b, _ := base64.RawURLEncoding.DecodeString(pp.RequestHeader)
				b = bytes.Replace(b, []byte(`"v":1`), []byte(`"v":2`), 1)
				pp.RequestHeader = base64.RawURLEncoding.EncodeToString(b)
			}
			if _, e := VerifyPoll(cc, rr, pp); e == nil {
				t.Fatal("forged request")
			}
		})
	}
}
func notificationPage() NotificationPage {
	return NotificationPage{Through: 99, Notifications: []Notification{}, Acknowledged: nil}
}
func TestNotificationEnvelope(t *testing.T) {
	c := notificationContext(t)
	root := bytes.Repeat([]byte{1}, 32)
	req := bytes.Repeat([]byte{2}, 32)
	page := notificationPage()
	env, e := SealNotification(c, root, req, page, fixedRandom(3))
	if e != nil {
		t.Fatal(e)
	}
	wire, e := EncodeNotificationEnvelope(env)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := DecodeNotificationEnvelope(wire)
	if e != nil {
		t.Fatal(e)
	}
	got, e := OpenNotification(c, root, req, decoded)
	if e != nil || got.Through != 99 || got.Notifications == nil || len(got.Notifications) != 0 {
		t.Fatal(got, e)
	}
	env2, e := SealNotification(c, root, req, page, fixedRandom(4))
	if e != nil || bytes.Equal(env.Ciphertext, env2.Ciphertext) {
		t.Fatal("reused reply key", e)
	}
	for _, mutation := range []string{"ciphertext", "reply", "request", "epoch", "ui", "host", "route", "root", "version"} {
		t.Run(mutation, func(t *testing.T) {
			wire, _ := EncodeNotificationEnvelope(env)
			ee, _ := DecodeNotificationEnvelope(wire)
			cc := c.Clone()
			rr := bytes.Clone(root)
			switch mutation {
			case "ciphertext":
				ee.Ciphertext[0] ^= 1
			case "reply":
				ee.Reply[0] ^= 1
			case "request":
				ee.Request[0] ^= 1
			case "epoch":
				ee.PairEpoch[0] ^= 1
			case "ui":
				ee.UIID = string(bytes.Repeat([]byte{'0'}, 96))
			case "host":
				ee.HostFP = string(bytes.Repeat([]byte{'0'}, 96))
			case "route":
				cc.Route.HostID = "other"
			case "root":
				rr[0] ^= 1
			case "version":
				ee.V = 2
			}
			if _, e := OpenNotification(cc, rr, req, ee); e == nil {
				t.Fatal("forged response")
			}
		})
	}
}
func TestNotificationPageCodec(t *testing.T) {
	page := NotificationPage{Batch: bytes.Repeat([]byte{3}, 16), Through: 99, Token: bytes.Repeat([]byte{4}, 32), Notifications: []Notification{{ID: 99, Ticket: 7, AuthorRole: "user", CreatedAt: "2026-01-01T00:00:00Z", Title: "a", Body: "b"}}, More: true, Acknowledged: bytes.Repeat([]byte{5}, 16)}
	b, e := EncodeNotificationPage(page)
	if e != nil {
		t.Fatal(e)
	}
	got, e := DecodeNotificationPage(b)
	if e != nil || got.Notifications[0].ID != 99 || !bytes.Equal(got.Token, page.Token) {
		t.Fatal(got, e)
	}
	page.Notifications = []Notification{{ID: 99, Unavailable: true}}
	b, e = EncodeNotificationPage(page)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = DecodeNotificationPage(b); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{
		`{"error":"invalid_ack","through":"1"}`, `{"error":"other"}`,
		`{"batch":null,"through":"0","token":null,"notifications":null,"more":false,"acknowledged":null}`,
		`{"batch":null,"through":"0","token":null,"notifications":[],"more":false,"more":true,"acknowledged":null}`,
		`{"batch":null,"through":"0","token":null,"notifications":[],"more":false,"acknowledged":"AA"}`,
	} {
		if _, e := DecodeNotificationPage([]byte(raw)); e == nil {
			t.Fatal("accepted malformed page", raw)
		}
	}
	page.Notifications[0].Title = "extra"
	if _, e := EncodeNotificationPage(page); e == nil {
		t.Fatal("unavailable extra fields")
	}
}
