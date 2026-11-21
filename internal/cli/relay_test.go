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

func TestRelayAcceptLeadingHyphenValues(t *testing.T) {
	isolate(t)
	old := newRelayClient
	t.Cleanup(func() { newRelayClient = old })
	// Canonical unpadded base64url for 16/1184 bytes starting with 0xf8.
	const aid = "-AAAAAAAAAAAAAAAAAAAAA"
	key := "-" + strings.Repeat("A", 1578)
	for _, name := range []string{"", "phone"} {
		for _, fullKey := range []bool{false, true} {
			mode := "PIN"
			want := api.RelayAcceptRequest{AID: aid, PIN: "007026", Name: name}
			args := []string{"relay", "pair", "accept"}
			if name != "" {
				args = append(args, "--name", name)
			}
			if fullKey {
				mode = "full-key"
				want.PIN, want.PublicKey = "", key
				args = append(args, "--public-key", key, "--", aid)
			} else {
				args = append(args, "--", aid, "007026")
			}
			t.Run(mode+"/"+name, func(t *testing.T) {
				var got api.RelayAcceptRequest
				calls := 0
				newRelayClient = func() (*apiclient.Client, error) {
					return apiclient.NewWithTransport(apiclient.HandlerTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.Method != http.MethodPost || r.URL.Path != "/api/relay/pair/accept" {
							t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
						}
						if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
							t.Fatal(err)
						}
						w.Header().Set("Content-Type", "application/json")
						json.NewEncoder(w).Encode(api.RelayAccepted{AID: aid, Device: api.RelayDevice{State: "active"}})
					})), ""), nil
				}
				if _, err := execute(t.Context(), args...); err != nil {
					t.Fatalf("accept command failed: %v", err)
				}
				if calls != 1 || got != want {
					t.Fatalf("accept request mismatch: calls=%d, aid=%q, pin=%q, name=%q, public key matches=%t", calls, got.AID, got.PIN, got.Name, got.PublicKey == want.PublicKey)
				}
			})
		}
	}
	for _, args := range [][]string{
		{"--", aid},
		{"--public-key", key, "--", aid, "007026"},
	} {
		newRelayClient = func() (*apiclient.Client, error) {
			t.Fatal("invalid credential selection reached the host")
			return nil, nil
		}
		if _, err := execute(t.Context(), append([]string{"relay", "pair", "accept"}, args...)...); err == nil || err.Error() != "provide either PIN or --public-key" {
			t.Fatalf("credential selection validation: %v", err)
		}
	}
}
