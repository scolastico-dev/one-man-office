//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func TestReloadFromOfficeDirectoryUsesUserSocketIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "omo.sock")
	called := make(chan string, 1)
	srv := sockd.New(socket, nil)
	srv.Handle("office.reload", func(agentID string, _ json.RawMessage) (any, error) {
		called <- agentID
		return proto.ConfigReloadResponse{Models: 3, Repos: 2}, nil
	})
	go srv.ListenAndServe()
	t.Cleanup(func() { _ = srv.Close() })
	deadline := time.Now().Add(2 * time.Second)
	for !sockExists(socket) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
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
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"reload"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := <-called; got != "user" {
		t.Fatalf("reload identity = %q", got)
	}
	if got := out.String(); got != "configuration reloaded: 3 models, 2 repositories\n" {
		t.Fatalf("reload output = %q", got)
	}
}

func sockExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
