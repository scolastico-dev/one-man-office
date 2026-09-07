//go:build !windows

package websupervisor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSupervisorBrowserWorkflowAndParentLoss(t *testing.T) {
	dir := projectHome(t)
	project, err := CreateProject(context.Background(), filepath.Join(dir, "office"), "")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "omo")
	build := exec.Command("go", "build", "-o", binary, "./cmd/omo")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", output, err)
	}
	cmd := exec.Command(binary, "supervisor", "--listen=127.0.0.1:0", "--max-agents=2", "--mock")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	processDone := make(chan struct{})
	go func() { _ = cmd.Wait(); close(processDone) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-processDone:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-processDone
		}
	})
	line := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line <- scanner.Text()
		}
	}()
	var access string
	select {
	case access = <-line:
	case <-time.After(10 * time.Second):
		t.Fatal("server did not print access URL")
	}
	u, err := url.Parse(strings.TrimPrefix(access, "omo supervisor: "))
	if err != nil {
		t.Fatal(err)
	}
	token := u.Fragment
	u.Fragment = ""
	base := u.String()
	api := func(method, path string, body any) []byte {
		t.Helper()
		data, _ := json.Marshal(body)
		request, _ := http.NewRequest(method, base+path, bytes.NewReader(data))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 10 * time.Second}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		output, _ := io.ReadAll(response.Body)
		if response.StatusCode >= 400 {
			t.Fatalf("%s %s HTTP %d: %s", method, path, response.StatusCode, output)
		}
		return output
	}
	launch := func(mode string) InstanceInfo {
		t.Helper()
		var info InstanceInfo
		if err := json.Unmarshal(api("POST", "api/instances", map[string]string{"path": project.Path, "mode": mode}), &info); err != nil {
			t.Fatal(err)
		}
		return info
	}
	connect := func(info InstanceInfo) *websocket.Conn {
		t.Helper()
		endpoint := *u
		endpoint.Scheme = "ws"
		endpoint.Path = "/api/instances/" + info.ID + "/terminal"
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{Subprotocols: []string{"omo", "omo-token." + token}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		return conn
	}
	readUntil := func(conn *websocket.Conn, want string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var output strings.Builder
		for !strings.Contains(output.String(), want) {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("terminal missing %q: %v; output %q", want, err, output.String())
			}
			output.Write(data)
		}
		return output.String()
	}
	shellA, shellB := launch("shell"), launch("shell")
	connA, connB := connect(shellA), connect(shellB)
	if shellA.ID == shellB.ID {
		t.Fatal("shells share identity")
	}
	if err := connA.Write(context.Background(), websocket.MessageText, []byte(`{"rows":41,"cols":111}`)); err != nil {
		t.Fatal(err)
	}
	command := "sleep 0.4; stty size; printf '\\nCLEAN:%s\\n' \"${OMO_CONTROL_TOKEN-unset}\"; printf 'END_A\\n'\n"
	if err := connA.Write(context.Background(), websocket.MessageBinary, []byte(command)); err != nil {
		t.Fatal(err)
	}
	output := readUntil(connA, "CLEAN:unset")
	if !strings.Contains(output, "41 111") {
		t.Fatalf("resize failed: %q", output)
	}
	if err := connB.Write(context.Background(), websocket.MessageBinary, []byte("printf '\\nSHELLPID:%s:END\\n' $$\n")); err != nil {
		t.Fatal(err)
	}
	output = readUntil(connB, ":END\r\n")
	start := strings.LastIndex(output, "SHELLPID:")
	if start < 0 {
		t.Fatalf("missing PID: %q", output)
	}
	pidText := strings.Split(output[start+len("SHELLPID:"):], ":")[0]
	shellPID, err := strconv.Atoi(pidText)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(shellPID, syscall.SIGKILL)
	api("POST", "api/instances/"+shellA.ID+"/kill", nil)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var snapshot struct {
			Instances []InstanceInfo `json:"instances"`
		}
		json.Unmarshal(api("GET", "api/state", nil), &snapshot)
		exited := false
		for _, i := range snapshot.Instances {
			if i.ID == shellA.ID {
				exited = i.State == "exited"
			}
			if i.ID == shellB.ID && i.State != "running" {
				t.Fatal("other shell stopped")
			}
		}
		if exited {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(project.Path, ".omo", "templates.sha256"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	office := launch("omo")
	officeConn := connect(office)
	output = readUntil(officeConn, "Run 'omo setup --update' and restart now?")
	if err := officeConn.Write(context.Background(), websocket.MessageBinary, []byte("n\n")); err != nil {
		t.Fatal(err)
	}
	duplicate := launch("omo")
	if office.ID != duplicate.ID {
		t.Fatal("duplicate office process")
	}
	deadline = time.Now().Add(15 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		var snapshot struct {
			Agents int `json:"agents"`
		}
		json.Unmarshal(api("GET", "api/state", nil), &snapshot)
		if snapshot.Agents == 1 {
			ready = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("mock office did not acquire CEO lease")
	}
	api("POST", "api/instances/"+office.ID+"/estop", nil)
	deadline = time.Now().Add(10 * time.Second)
	released := false
	for time.Now().Before(deadline) {
		var snapshot struct {
			Agents    int            `json:"agents"`
			Instances []InstanceInfo `json:"instances"`
		}
		json.Unmarshal(api("GET", "api/state", nil), &snapshot)
		for _, i := range snapshot.Instances {
			if i.ID == office.ID && i.State == "exited" && snapshot.Agents == 0 {
				released = true
			}
		}
		if released {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !released {
		t.Fatal("estop did not exit office and release its leases")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-processDone
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(shellPID, 0); err != nil {
			return
		}
		stat, _ := os.ReadFile("/proc/" + strconv.Itoa(shellPID) + "/stat")
		if strings.Contains(string(stat), ") Z ") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("shell %d survived parent supervisor loss", shellPID)
}

func TestSupervisorShutdownCancelsActiveProjectClone(t *testing.T) {
	dir := projectHome(t)
	gitDir := filepath.Join(dir, "git-bin")
	if err := os.Mkdir(gitDir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "clone-started")
	script := "#!/bin/sh\nprintf '%s' \"$$\" > \"$OMO_TEST_CLONE_MARKER\"\nexec sleep 300\n"
	if err := os.WriteFile(filepath.Join(gitDir, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", gitDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OMO_TEST_CLONE_MARKER", marker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, Options{Listen: "127.0.0.1:0", MaxAgents: 1}, writer) }()
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(strings.TrimSpace(strings.TrimPrefix(line, "omo supervisor: ")))
	if err != nil {
		t.Fatal(err)
	}
	token := u.Fragment
	u.Fragment = ""
	u.Path = "/api/projects"
	requestCtx, requestCancel := context.WithCancel(context.Background())
	defer requestCancel()
	body, _ := json.Marshal(map[string]string{"action": "clone", "path": filepath.Join(dir, "clone"), "source": "https://example.invalid/repository.git"})
	request, _ := http.NewRequestWithContext(requestCtx, "POST", u.String(), bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			response.Body.Close()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil {
			pid, _ = strconv.Atoi(string(data))
			if pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("clone did not start")
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not cancel active clone during shutdown")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("clone request did not close")
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatal("clone process survived supervisor shutdown")
	}
}
