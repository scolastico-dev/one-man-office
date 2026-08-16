//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func TestTypeFromOfficeDirectoryUsesUserIdentityAndPreservesKeyOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "omo.sock")
	type received struct {
		agentID string
		args    proto.AgentInputArgs
	}
	calls := make(chan received, 1)
	srv := sockd.New(socket, nil)
	srv.Handle("agent.input", func(agentID string, raw json.RawMessage) (any, error) {
		var args proto.AgentInputArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		calls <- received{agentID: agentID, args: args}
		return nil, nil
	})
	go srv.ListenAndServe()
	t.Cleanup(func() { _ = srv.Close() })
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket did not start")
		}
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
	cmd.SetArgs([]string{"type", "developer-ada", "1", "--key", "down,enter"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	call := <-calls
	if call.agentID != "user" {
		t.Fatalf("input identity = %q", call.agentID)
	}
	want := proto.AgentInputArgs{Name: "developer-ada", Text: "1", Keys: []string{"down", "enter"}}
	if !reflect.DeepEqual(call.args, want) {
		t.Fatalf("input request = %+v, want %+v", call.args, want)
	}
	if got := out.String(); got != "input sent to developer-ada\n" {
		t.Fatalf("type output = %q", got)
	}
}

func TestTypeRequiresTextOrSpecialKey(t *testing.T) {
	cmd := Root("test")
	cmd.SetArgs([]string{"type", "developer-ada"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("empty agent input accepted")
	}
}
