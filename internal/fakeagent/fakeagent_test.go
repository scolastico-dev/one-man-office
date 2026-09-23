package fakeagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

// TestScenarioDrivesVerbs runs a scenario against a stub socket server and
// asserts the verbs arrive in order with substituted args.
func TestScenarioDrivesVerbs(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "omo.sock")
	var calls []string
	srv := sockd.New(sock, nil)
	srv.Handle("ready", func(id string, _ json.RawMessage) (any, error) {
		calls = append(calls, "ready:"+id)
		return proto.ReadyResponse{Prompt: "go", JobID: 42}, nil
	})
	srv.Handle("send", func(_ string, args json.RawMessage) (any, error) {
		var a proto.SendArgs
		json.Unmarshal(args, &a)
		calls = append(calls, "send:"+a.To+":"+a.Subject)
		return map[string]int{"delivered": 1}, nil
	})
	srv.Handle("job.create", func(_ string, args json.RawMessage) (any, error) {
		var a proto.JobCreateArgs
		json.Unmarshal(args, &a)
		calls = append(calls, "jobcreate:"+a.Role+":parent="+strconv.FormatInt(a.Parent, 10))
		return proto.JobCreateResponse{ID: 43}, nil
	})
	srv.Handle("done", func(_ string, args json.RawMessage) (any, error) {
		var a proto.DoneArgs
		json.Unmarshal(args, &a)
		calls = append(calls, "done:"+a.Result)
		return nil, nil
	})
	go srv.ListenAndServe()
	defer srv.Close()
	time.Sleep(100 * time.Millisecond)

	scenario := filepath.Join(dir, "s.txt")
	os.WriteFile(scenario, []byte(
		"# demo scenario\nready\nsleep|10ms\nsend|ceo|hello|from fake\njobcreate|developer|t|g||$JOB\ndone|ok\n",
	), 0o644)
	t.Setenv("OMO_SOCKET", sock)
	t.Setenv("OMO_AGENT_ID", "pm-alex")
	if err := Run(os.Stderr, scenario, ""); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls, " ")
	want := "ready:pm-alex send:ceo:hello jobcreate:developer:parent=42 done:ok"
	if got != want {
		t.Fatalf("calls:\n got: %s\nwant: %s", got, want)
	}
}

func TestScenarioWaiterrorAcceptsExpectedWaitFailure(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "omo.sock")
	srv := sockd.New(sock, nil)
	srv.Handle("ready", func(string, json.RawMessage) (any, error) {
		return proto.ReadyResponse{Prompt: "go"}, nil
	})
	srv.Handle("wait", func(string, json.RawMessage) (any, error) {
		return nil, errors.New("a firefighter never parks: finish now")
	})
	go srv.ListenAndServe()
	defer srv.Close()
	time.Sleep(100 * time.Millisecond)

	scenario := filepath.Join(dir, "s.txt")
	if err := os.WriteFile(scenario, []byte("ready\nwaiterror|a firefighter never parks\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_SOCKET", sock)
	t.Setenv("OMO_AGENT_ID", "firefighter-ada")
	if err := Run(os.Stderr, scenario, ""); err != nil {
		t.Fatal(err)
	}
}
