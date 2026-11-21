package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func TestRelayAdministrationRequiresSocketHuman(t *testing.T) {
	s := newTestServer(t)
	browser := newBrowser(t, s)
	session := liveSession(t, s, api.RoleDeveloper)
	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/relay/status", nil}, {"POST", "/api/relay/connect", map[string]any{"origin": "https://relay.example", "client_id": "office", "password": "secret"}},
		{"POST", "/api/relay/disconnect", map[string]any{}}, {"POST", "/api/relay/pair/begin", map[string]any{}}, {"GET", "/api/relay/pair/pending", nil},
		{"POST", "/api/relay/pair/accept", map[string]any{"aid": "bad", "pin": "123456"}}, {"POST", "/api/relay/pair/cancel", map[string]any{}},
		{"GET", "/api/relay/devices", nil}, {"DELETE", "/api/relay/devices/unknown", nil},
	}
	for _, row := range routes {
		t.Run(row.method+row.path, func(t *testing.T) {
			r := socketDo(t, s, row.method, row.path, row.body, session.ID)
			wantStatus(t, r.Code, http.StatusForbidden)
			response := browser.do(row.method, row.path, row.body, bearer(s))
			wantStatus(t, response.StatusCode, http.StatusForbidden)
			response.Body.Close()
			// Merely having the human board role does not create local provenance.
			req := withRequestContext(newRequest(t, row.method, "http://omo"+row.path, row.body), board.User, s.log)
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)
			wantStatus(t, rec.Code, http.StatusForbidden)
		})
	}
	// Header presence, including a deliberately noncanonical empty header, must
	// never be interpreted as socket-human provenance.
	for _, key := range []string{"X-Omo-Session", "x-omo-session", "X-OMO-SESSION"} {
		r := httptest.NewRequest("GET", "http://omo/api/relay/status", nil)
		r.Header[key] = []string{""}
		rec := httptest.NewRecorder()
		s.SocketHandler().ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatal(key, rec.Code)
		}
	}
}
func TestRelayLocalControlsPersistAndHideSecrets(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	client := apiclient.NewWithTransport(apiclient.HandlerTransport(s.SocketHandler()), "")
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer relay.Close()
	secret := "private-admission"
	status, e := client.RelayConnect(ctx, api.RelayConnectRequest{Origin: relay.URL, ClientID: "office", Password: &secret})
	if e != nil {
		t.Fatal(e)
	}
	if status.HostID != "office" || status.Origin != relay.URL || !status.Enabled {
		t.Fatal(status)
	}
	if strings.Contains(string(client.LastBody()), secret) || strings.Contains(string(client.LastBody()), "seed") {
		t.Fatal("secret in local response")
	}
	c, e := serverconfig.Load(s.opts.Layout.ServerConfig)
	if e != nil || !c.RelayEnabled || c.RelayURL != status.Origin {
		t.Fatal(c, e)
	}
	if e := client.RelayPairBegin(ctx); e != nil {
		t.Fatal(e)
	}
	pending, e := client.RelayPairPending(ctx)
	if e != nil || pending.Phase != "armed" {
		t.Fatal(pending, e)
	}
	if e := client.RelayPairCancel(ctx); e != nil {
		t.Fatal(e)
	}
	list, e := client.RelayDevices(ctx)
	if e != nil || list.Devices == nil || len(list.Devices) != 0 {
		t.Fatal(list, e)
	}
	if e := client.RelayDisconnect(ctx); e != nil {
		t.Fatal(e)
	}
	status, e = client.RelayStatus(ctx)
	if e != nil || status.Enabled {
		t.Fatal(status, e)
	}
	c, e = serverconfig.Load(s.opts.Layout.ServerConfig)
	if e != nil || c.RelayEnabled {
		t.Fatal(c, e)
	}
}

func TestRelayEncodedAcceptanceValuesPreservePresence(t *testing.T) {
	// Invalid nonempty keys must remain a failed full-key attempt, including
	// when a valid PIN was also supplied, instead of selecting PIN-only mode.
	for _, text := range []string{"!", "AA=", "AB", "AA\n", "fingerprint:abcdef"} {
		if got := decodeRelayBytes(text); len(got) == 0 || len(got) == 16 || len(got) == 1184 {
			t.Fatalf("invalid supplied value became absent or a valid fixed-length value: %q", text)
		}
	}
	if got := decodeRelayBytes(""); len(got) != 0 {
		t.Fatal("absent option became present")
	}
}
