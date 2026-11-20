package relayserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

func pollRequest(ctx context.Context, addr string) *http.Request {
	r, _ := http.NewRequestWithContext(ctx, "GET", addr+"/notifications/office/"+strings.Repeat("a", 96), nil)
	// Intentionally not endpoint JSON: the relay checks syntax, not content.
	r.Header.Set("Omo-Notification-Request", base64.RawURLEncoding.EncodeToString([]byte("opaque request")))
	r.Header.Set("Omo-Notification-MAC", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 48)))
	return r
}

type httpResult struct {
	response *http.Response
	err      error
}

func startPoll(t *testing.T, req *http.Request) <-chan httpResult {
	t.Helper()
	ctx, cancel := context.WithCancel(req.Context())
	req = req.Clone(ctx)
	result := make(chan httpResult, 1)
	go func() { defer close(result); r, err := http.DefaultClient.Do(req); result <- httpResult{r, err} }()
	t.Cleanup(func() {
		cancel()
		for r := range result {
			if r.response != nil {
				r.response.Body.Close()
			}
		}
	})
	return result
}

func checkPoll(t *testing.T, ch <-chan httpResult, status int, body []byte) {
	t.Helper()
	result := <-ch
	if result.err != nil {
		t.Fatal(result.err)
	}
	defer result.response.Body.Close()
	b, err := io.ReadAll(result.response.Body)
	if err != nil || result.response.StatusCode != status || !bytes.Equal(b, body) {
		t.Fatalf("poll: status=%d body=%q err=%v", result.response.StatusCode, b, err)
	}
	if result.response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	if status == 200 && result.response.Header.Get("Content-Type") != "application/vnd.omo.relay-notifications+json" {
		t.Fatal("wrong content type")
	}
}

func TestNotificationCorrelation(t *testing.T) {
	_, addr := testRelay(t, Config{})
	h := host(t, addr)
	other := dial(t, addr, nil)
	send(t, other, relayproto.Control{Type: "host.register", HostID: "other"})
	receive(t, other, "host.registered")
	r1, r2 := pollRequest(testContext(t), addr), pollRequest(testContext(t), addr)
	one := startPoll(t, r1)
	m1 := receive(t, h, "notification.get")
	two := startPoll(t, r2)
	m2 := receive(t, h, "notification.get")
	if bytes.Equal(m1.QID, m2.QID) {
		t.Fatal("qid reuse")
	}
	if m1.RequestHeader != r1.Header.Get("Omo-Notification-Request") || m1.RequestMAC != r1.Header.Get("Omo-Notification-MAC") || m1.HostID != "office" || m1.UIID != strings.Repeat("a", 96) {
		t.Fatalf("changed endpoint headers: %+v", m1)
	}
	// A foreign host cannot answer the first request. Reading its next registered
	// UI event is a barrier proving its forged reply was processed first.
	send(t, other, relayproto.Control{Type: "notification.reply", QID: m1.QID, Body: []byte("forged")})
	u := dial(t, addr, nil)
	send(t, u, relayproto.Control{Type: "ui.attach", HostID: "other"})
	receive(t, u, "ui.attached")
	receive(t, other, "ui.open")
	send(t, h, relayproto.Control{Type: "notification.reply", QID: m2.QID, Body: []byte("second opaque reply")})
	checkPoll(t, two, 200, []byte("second opaque reply"))
	send(t, h, relayproto.Control{Type: "notification.reply", QID: m1.QID, Body: []byte("first opaque reply")})
	checkPoll(t, one, 200, []byte("first opaque reply"))
	// Duplicate and late replies are ignored, and cannot be consumed by a new GET.
	send(t, h, relayproto.Control{Type: "notification.reply", QID: m1.QID, Body: []byte("duplicate")})
	three := startPoll(t, pollRequest(testContext(t), addr))
	m3 := receive(t, h, "notification.get")
	send(t, h, relayproto.Control{Type: "notification.empty", QID: m3.QID})
	checkPoll(t, three, 204, nil)
}

func TestNotificationCancellationAndHostLoss(t *testing.T) {
	_, addr := testRelay(t, Config{})
	h := host(t, addr)
	ctx, cancel := context.WithCancel(testContext(t))
	result := startPoll(t, pollRequest(ctx, addr))
	m := receive(t, h, "notification.get")
	cancel()
	if response := <-result; response.err == nil {
		response.response.Body.Close()
		t.Fatal("cancelled poll succeeded")
	}
	if cancelled := receive(t, h, "notification.cancel"); !bytes.Equal(m.QID, cancelled.QID) {
		t.Fatal("cancelled wrong qid")
	}
	send(t, h, relayproto.Control{Type: "notification.reply", QID: m.QID, Body: []byte("late")})
	result = startPoll(t, pollRequest(testContext(t), addr))
	receive(t, h, "notification.get")
	h.CloseNow()
	checkPoll(t, result, 204, nil)
	checkPoll(t, startPoll(t, pollRequest(testContext(t), addr)), 204, nil)
	host(t, addr)
}

func TestNotificationValidation(t *testing.T) {
	_, addr := testRelay(t, Config{})
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.URL.Path = "/notifications/invalid!/" + strings.Repeat("a", 96) },
		func(r *http.Request) { r.URL.Path = "/notifications/office/not-a-ui" },
		func(r *http.Request) { r.URL.Path += "/extra" },
		func(r *http.Request) { r.Header.Del("Omo-Notification-Request") },
		func(r *http.Request) { r.Header.Add("Omo-Notification-MAC", r.Header.Get("Omo-Notification-MAC")) },
		func(r *http.Request) { r.Header.Set("Omo-Notification-MAC", "bad") },
		func(r *http.Request) { r.Header.Set("Omo-Notification-Request", strings.Repeat("A", 4100)) },
		func(r *http.Request) { r.URL.RawQuery = "password=forbidden" },
	} {
		r := pollRequest(testContext(t), addr)
		change(r)
		checkPoll(t, startPoll(t, r), 400, nil)
	}
}

func TestNotificationTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := New(Config{})
		defer s.Close()
		h, _ := fakeHost(t, s)
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { defer close(done); s.ServeHTTP(w, pollRequest(context.Background(), "http://relay")) }()
		synctest.Wait()
		get, _ := relayproto.DecodeControl(<-h.out)
		if get.Type != "notification.get" {
			t.Fatalf("got %+v", get)
		}
		// synctest advances virtual time, never a real-time pass condition.
		time.Sleep(30 * time.Second)
		<-done
		if w.Code != 204 {
			t.Fatalf("timeout status: %d", w.Code)
		}
		cancel, _ := relayproto.DecodeControl(<-h.out)
		if cancel.Type != "notification.cancel" || !bytes.Equal(cancel.QID, get.QID) {
			t.Fatalf("cancel: %+v", cancel)
		}
		s.mu.Lock()
		pending := len(h.pending)
		s.mu.Unlock()
		if pending != 0 {
			t.Fatal("timeout retained qid")
		}
	})
}

func TestExpiredNotificationCannotAcceptLateReply(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := New(Config{})
		defer s.Close()
		h, _ := fakeHost(t, s)
		ctx, cancel := context.WithCancel(context.Background())
		n := &notification{reply: make(chan []byte, 1), ctx: ctx}
		qid := bytes.Repeat([]byte{5}, 16)
		s.mu.Lock()
		h.pending[string(qid)] = n
		s.mu.Unlock()
		cancel()
		// Hold the HTTP cleanup behind this exact barrier: a cancelled request
		// must not accept a reply even before its handler gets scheduled again.
		s.route(h, relayproto.Control{V: 1, Type: "notification.reply", QID: qid, Body: []byte("late")})
		select {
		case b := <-n.reply:
			t.Fatalf("accepted cancelled response %q", b)
		default:
		}
		s.cancelNotification(h, qid, n)
	})
}
