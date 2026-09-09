//go:build !windows

package websupervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecuteReapsDescendantsAfterCommandExit(t *testing.T) {
	s, server := testServer(t)
	body, _ := json.Marshal(executeRequest{Command: os.Args[0], Args: []string{"-test.run=^TestExecuteDescendantHelper$"}})
	status, data := requestAPI(t, s, server, "POST", "/api/commands", string(body))
	if status != 200 {
		t.Fatalf("execute: HTTP %d %s", status, data)
	}
	var pid int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event commandEvent
		if json.Unmarshal([]byte(line), &event) == nil && event.Stream == "stdout" {
			pid, _ = strconv.Atoi(strings.TrimSpace(event.Data))
		}
	}
	if pid < 1 {
		t.Fatalf("missing descendant pid: %s", data)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("command descendant %d survived request completion", pid)
}

func TestExecuteDescendantHelper(t *testing.T) {
	if os.Getenv("OMO_SUPERVISOR") != "1" {
		return
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, child.Process.Pid)
	os.Exit(0)
}
