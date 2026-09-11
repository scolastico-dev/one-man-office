package company

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/proto"
)

func TestInstanceTUIForwardsUserVerbAndPayload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket test")
	}
	projectHome(t)
	s, ts := testServer(t)
	officeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(officeDir, ".omo"), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(officeDir, "tui.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	if err := os.WriteFile(filepath.Join(officeDir, office.LockPath), []byte(listener.Addr().String()), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan proto.Request, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var request proto.Request
				if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&request); err != nil {
					return
				}
				if request.Verb == "tui.show" {
					requests <- request
					_ = json.NewEncoder(conn).Encode(proto.Response{OK: true})
				}
			}()
		}
	}()
	s.instances["office-1"] = &Instance{info: InstanceInfo{ID: "office-1", Path: officeDir, Mode: "omo", State: "running", Started: time.Now()}}
	t.Cleanup(func() { s.mu.Lock(); delete(s.instances, "office-1"); s.mu.Unlock() })

	status, body := requestAPI(t, s, ts, http.MethodPost, "/api/instances/office-1/tui", `{"agent":"developer-ada"}`)
	if status != http.StatusOK {
		t.Fatalf("tui: HTTP %d %s", status, body)
	}
	select {
	case request := <-requests:
		if request.AgentID != "user" || request.Verb != "tui.show" {
			t.Fatalf("forwarded request = %+v", request)
		}
		var args proto.TUIShowArgs
		if err := json.Unmarshal(request.Args, &args); err != nil || args.Agent != "developer-ada" {
			t.Fatalf("forwarded args = %s: %v", request.Args, err)
		}
	case <-time.After(time.Second):
		t.Fatal("socket request was not forwarded")
	}
}

func TestInstanceTUIRejectsNonStrictJSONAndUnavailableInstances(t *testing.T) {
	projectHome(t)
	s, ts := testServer(t)
	t.Cleanup(func() {
		s.mu.Lock()
		for _, id := range []string{"shell", "setup", "exited", "unready"} {
			delete(s.instances, id)
		}
		s.mu.Unlock()
	})
	status, _ := requestAPI(t, s, ts, http.MethodPost, "/api/instances/missing/tui", `{"agent":"x"}`)
	if status != http.StatusConflict {
		t.Fatalf("missing instance: HTTP %d", status)
	}
	status, _ = requestAPI(t, s, ts, http.MethodPost, "/api/instances/missing/tui", `{"agent":"x","extra":true}`)
	if status != http.StatusBadRequest {
		t.Fatalf("unknown field: HTTP %d", status)
	}
	for _, tc := range []struct {
		name  string
		mode  string
		state string
	}{
		{name: "shell", mode: "shell", state: "running"},
		{name: "setup", mode: "setup", state: "running"},
		{name: "exited", mode: "omo", state: "exited"},
		{name: "unready", mode: "omo", state: "running"},
	} {
		dir := t.TempDir()
		if tc.name == "unready" {
			if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, office.LockPath), []byte(filepath.Join(dir, "missing.sock")), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		s.instances[tc.name] = &Instance{info: InstanceInfo{ID: tc.name, Path: dir, Mode: tc.mode, State: tc.state}}
		status, _ := requestAPI(t, s, ts, http.MethodPost, "/api/instances/"+tc.name+"/tui", `{"agent":"x"}`)
		if status != http.StatusConflict {
			t.Fatalf("%s instance: HTTP %d", tc.name, status)
		}
	}
}

func TestInstanceTUIMapsTimeoutAndChildError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket test")
	}
	for _, tc := range []struct {
		name       string
		wantStatus int
		response   proto.Response
		hold       bool
	}{
		{name: "timeout", wantStatus: http.StatusGatewayTimeout, hold: true},
		{name: "child error", wantStatus: http.StatusBadGateway, response: proto.Response{Error: "tui not attached"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projectHome(t)
			s, ts := testServer(t)
			officeDir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(officeDir, ".omo"), 0o700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", filepath.Join(officeDir, "tui.sock"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { listener.Close() })
			if err := os.WriteFile(filepath.Join(officeDir, office.LockPath), []byte(listener.Addr().String()), 0o600); err != nil {
				t.Fatal(err)
			}
			go func() {
				var connections int
				for {
					conn, acceptErr := listener.Accept()
					if acceptErr != nil {
						return
					}
					connections++
					if connections == 1 {
						_ = conn.Close()
						continue
					}
					if tc.hold {
						time.Sleep(4 * time.Second)
						_ = conn.Close()
						continue
					}
					var request proto.Request
					_ = json.NewDecoder(conn).Decode(&request)
					_ = json.NewEncoder(conn).Encode(tc.response)
					_ = conn.Close()
				}
			}()
			s.instances["office-1"] = &Instance{info: InstanceInfo{ID: "office-1", Path: officeDir, Mode: "omo", State: "running"}}
			t.Cleanup(func() {
				s.mu.Lock()
				delete(s.instances, "office-1")
				s.mu.Unlock()
			})
			status, body := requestAPI(t, s, ts, http.MethodPost, "/api/instances/office-1/tui", `{"agent":"x"}`)
			if status != tc.wantStatus {
				t.Fatalf("%s: HTTP %d %s, want %d", tc.name, status, body, tc.wantStatus)
			}
			if tc.response.Error != "" && !strings.Contains(string(body), tc.response.Error) {
				t.Fatalf("%s: body %q does not contain child error", tc.name, body)
			}
		})
	}
}
