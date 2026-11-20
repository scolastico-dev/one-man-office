package relayserver

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// TestDeploymentProbe also runs against the actual executable/image from
// docker/relay_test.sh. Normal Go tests use an isolated in-process relay.
func TestDeploymentProbe(t *testing.T) {
	addr := os.Getenv("RELAY_TEST_URL")
	secretFile := os.Getenv("RELAY_TEST_PASSWORD_FILE")
	if addr == "" {
		_, addr = testRelay(t, Config{})
		secretFile = ""
	}
	var admission http.Header
	if secretFile != "" {
		secret, err := os.ReadFile(secretFile)
		if err != nil {
			t.Fatal("cannot read deployment probe secret")
		}
		r, _ := http.NewRequest("GET", addr, nil)
		r.SetBasicAuth("relay", strings.TrimSuffix(string(secret), "\n"))
		admission = r.Header
	}
	for _, path := range []string{"/v1/ws", "/notifications/office/" + strings.Repeat("a", 96)} {
		for _, wrong := range []bool{false, true} {
			r, _ := http.NewRequestWithContext(testContext(t), "GET", addr+path, nil)
			if wrong {
				r.SetBasicAuth("relay", "definitely-wrong")
			}
			resp, err := http.DefaultClient.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if (resp.StatusCode == 401) != (secretFile != "") {
				t.Fatalf("deployment admission %s: %d", path, resp.StatusCode)
			}
			if len(body) != 0 && resp.StatusCode == 401 {
				t.Fatal("admission echoed content")
			}
		}
	}
	h := dial(t, addr, admission)
	send(t, h, relayproto.Control{Type: "host.register", HostID: "office"})
	receive(t, h, "host.registered")
	u := dial(t, addr, admission)
	send(t, u, relayproto.Control{Type: "ui.attach", HostID: "office"})
	attached := receive(t, u, "ui.attached")
	opened := receive(t, h, "ui.open")
	if !bytes.Equal(opened.Connection, attached.Connection) {
		t.Fatal("deployment routing handle mismatch")
	}
	send(t, u, relayproto.Control{Type: "forward", Connection: attached.Connection, Payload: []byte("deployment opaque payload")})
	m := receive(t, h, "forward")
	send(t, h, m)
	if m = receive(t, u, "forward"); string(m.Payload) != "deployment opaque payload" {
		t.Fatal("deployment payload changed")
	}
	r := pollRequest(testContext(t), addr)
	if admission != nil {
		r.Header.Set("Authorization", admission.Get("Authorization"))
	}
	result := startPoll(t, r)
	get := receive(t, h, "notification.get")
	send(t, h, relayproto.Control{Type: "notification.reply", QID: get.QID, Body: []byte("opaque encrypted notification envelope")})
	checkPoll(t, result, 200, []byte("opaque encrypted notification envelope"))
	h.CloseNow()
	if _, _, err := u.Read(testContext(t)); err == nil {
		t.Fatal("deployment host loss retained UI")
	}
	r = pollRequest(testContext(t), addr)
	if admission != nil {
		r.Header.Set("Authorization", admission.Get("Authorization"))
	}
	checkPoll(t, startPoll(t, r), 204, nil)
}
