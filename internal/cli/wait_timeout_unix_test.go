//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func TestWaitTimeoutFlagIsSentToSupervisor(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "omo.sock")
	requests := make(chan proto.WaitArgs, 1)
	srv := sockd.New(socket, nil)
	srv.Handle("wait", func(_ string, raw json.RawMessage) (any, error) {
		var args proto.WaitArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		requests <- args
		return proto.WaitResponse{Reason: "timeout"}, nil
	})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })
	t.Setenv("OMO_SOCKET", socket)
	t.Setenv("OMO_AGENT_ID", "pm-test")

	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"wait", "--timeout", "1500ms"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := <-requests; got.TimeoutMillis != 1500 {
		t.Fatalf("timeout millis = %d", got.TimeoutMillis)
	}
	if got := out.String(); got != "wait timed out\n" {
		t.Fatalf("output = %q", got)
	}
}
