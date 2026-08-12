//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func TestJobCreateGoalFileCopiesContentsIntoRequest(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "omo.sock")
	srv := sockd.New(sock, nil)
	received := make(chan proto.JobCreateArgs, 1)
	srv.Handle("job.create", func(_ string, raw json.RawMessage) (any, error) {
		var args proto.JobCreateArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		received <- args
		return proto.JobCreateResponse{ID: 42}, nil
	})
	go srv.ListenAndServe()
	t.Cleanup(func() { _ = srv.Close() })
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Setenv("OMO_SOCKET", sock)
	t.Setenv("OMO_AGENT_ID", "ceo-ada")

	goal := "Investigate the queue.\n\nReturn evidence and a recommendation.\n"
	goalPath := filepath.Join(t.TempDir(), "goal.md")
	if err := os.WriteFile(goalPath, []byte(goal), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"job", "create", "--role", "freelancer", "--title", "queue research", "--goal-file", goalPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job create: %v\n%s", err, out.String())
	}

	select {
	case args := <-received:
		if args.Goal != goal {
			t.Fatalf("request goal = %q, want exact file contents %q", args.Goal, goal)
		}
	case <-time.After(time.Second):
		t.Fatal("job.create request not received")
	}
}

func TestJobCreateRejectsGoalAndGoalFileTogether(t *testing.T) {
	goalPath := filepath.Join(t.TempDir(), "goal.md")
	if err := os.WriteFile(goalPath, []byte("goal"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := Root("test")
	cmd.SetArgs([]string{"job", "create", "--role", "freelancer", "--title", "x", "--goal", "inline", "--goal-file", goalPath})
	if err := cmd.Execute(); err == nil {
		t.Fatal("--goal and --goal-file must be mutually exclusive")
	}
}
