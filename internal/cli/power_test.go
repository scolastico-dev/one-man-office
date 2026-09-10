package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func TestSafeShutdownCommandSendsReasonPayload(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "reason", args: []string{"safe-shutdown", "--reason", "maintenance window"}, want: "maintenance window"},
		{name: "blank by default", args: []string{"safe-shutdown"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o755); err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(t.TempDir(), "omo.sock")
			payloads := make(chan []byte, 1)
			srv := sockd.New(socket, nil)
			srv.Handle("office.safe-shutdown", func(_ string, args json.RawMessage) (any, error) {
				payloads <- append([]byte(nil), args...)
				return nil, nil
			})
			if err := srv.Listen(); err != nil {
				t.Fatal(err)
			}
			go srv.Serve()
			t.Cleanup(func() { _ = srv.Close() })
			if err := os.WriteFile(filepath.Join(dir, office.LockPath), []byte(socket+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("OMO_SOCKET", "")
			t.Setenv("OMO_AGENT_ID", "")
			t.Setenv("OMO_OFFICE_DIR", dir)
			t.Setenv("OMO_PLUGIN_NAME", "")

			cmd := Root("test")
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var raw []byte
			select {
			case raw = <-payloads:
			case <-time.After(2 * time.Second):
				t.Fatal("safe-shutdown payload was not received")
			}
			var got proto.SafeShutdownArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
			}
			if got.Reason != tt.want {
				t.Fatalf("safe-shutdown reason = %q, want %q", got.Reason, tt.want)
			}
		})
	}
}
