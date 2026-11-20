package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

func TestRelayHumanCommands(t *testing.T) {
	isolate(t)
	old := newRelayClient
	t.Cleanup(func() { newRelayClient = old })
	var paths []string
	var connect api.RelayConnectRequest
	var accept api.RelayAcceptRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/relay/connect":
			json.NewDecoder(r.Body).Decode(&connect)
			json.NewEncoder(w).Encode(api.RelayStatus{State: "retrying", HostID: "office", HostFP: "host", Enabled: true})
		case "/api/relay/status":
			json.NewEncoder(w).Encode(api.RelayStatus{State: "collision", HostID: "office", HostFP: "host", RetrySeconds: 30})
		case "/api/relay/pair/pending":
			json.NewEncoder(w).Encode(api.RelayPending{Phase: "proved", AID: "attempt", UIID: "device"})
		case "/api/relay/pair/accept":
			json.NewDecoder(r.Body).Decode(&accept)
			json.NewEncoder(w).Encode(api.RelayAccepted{AID: "attempt", HostFP: "host", Device: api.RelayDevice{UIID: "device", State: "active"}})
		case "/api/relay/devices":
			json.NewEncoder(w).Encode(api.RelayDeviceList{Devices: []api.RelayDevice{{UIID: "device", Name: "phone", State: "active", LiveConnections: 2}}})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	newRelayClient = func() (*apiclient.Client, error) {
		return apiclient.NewWithTransport(apiclient.HandlerTransport(handler), ""), nil
	}
	for _, args := range [][]string{{"connect", "https://relay.example", "--client-id", "office", "--password-stdin"}, {"status"}, {"pair", "begin"}, {"pair", "pending"}, {"pair", "accept", "attempt", "123456", "--name", "phone"}, {"pair", "accept", "attempt", "--public-key", "full-key", "--name", "tablet"}, {"pair", "cancel"}, {"devices"}, {"unregister", "device"}, {"disconnect"}} {
		cmd := Root("test")
		cmd.SetArgs(append([]string{"relay"}, args...))
		cmd.SetIn(strings.NewReader("secret-password\n"))
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if e := cmd.ExecuteContext(context.Background()); e != nil {
			t.Fatal(args, e)
		}
		if strings.Contains(out.String(), "secret-password") {
			t.Fatal("printed admission secret")
		}
	}
	if connect.Password == nil || *connect.Password != "secret-password" || connect.ClientID != "office" {
		t.Fatal("password/id not sent")
	}
	if accept.PublicKey != "full-key" || accept.PIN != "" || accept.Name != "tablet" {
		t.Fatal(accept)
	}
	if len(paths) != 10 {
		t.Fatal(paths)
	}
}
func TestRelayConfigCommandValues(t *testing.T) {
	isolate(t)
	mustExecute(t, "config", "set", "relay_url", "https://RELAY.example:443")
	if got := mustExecute(t, "config", "get", "relay_url"); strings.TrimSpace(got) != "https://relay.example" {
		t.Fatal(got)
	}
	if got := mustExecute(t, "config", "get", "relay_enabled"); strings.TrimSpace(got) != "false" {
		t.Fatal(got)
	}
}
