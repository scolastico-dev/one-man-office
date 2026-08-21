//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func TestJobCancelFromOfficeDirectoryUsesUserIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "omo.sock")
	called := make(chan string, 1)
	srv := sockd.New(socket, nil)
	srv.Handle("job.cancel", func(agentID string, args json.RawMessage) (any, error) {
		var request proto.JobIDArgs
		if err := json.Unmarshal(args, &request); err != nil {
			return nil, err
		}
		if request.ID != 42 {
			t.Fatalf("job id = %d", request.ID)
		}
		called <- agentID
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
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	t.Setenv("OMO_SOCKET", "")
	t.Setenv("OMO_AGENT_ID", "")

	cmd := Root("test")
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"job", "cancel", "42"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := <-called; got != "user" {
		t.Fatalf("job cancel identity = %q", got)
	}
}
