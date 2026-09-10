//go:build !windows

package supervisorservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	"github.com/scolastico-dev/one-man-office/internal/websupervisor"
)

func TestDetachedCLIReadinessStopAndOwnedShellCleanup(t *testing.T) {
	dir := isolatedHome(t)
	binary := filepath.Join(t.TempDir(), "omo")
	build := exec.Command("go", "build", "-o", binary, "./cmd/omo")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", output, err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		return string(output), err
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = Stop(ctx)
	})
	output, err := run("supervisor", "-d", "--listen=127.0.0.1:0", "--max-agents=4", "--mock")
	if err != nil || !strings.Contains(output, "started in the background") {
		t.Fatalf("detach: %s %v", output, err)
	}
	state := awaitState(t, dir)
	if !strings.Contains(output, state.URL) {
		t.Fatalf("missing access URL: %s", output)
	}
	u, err := url.Parse(state.URL)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Fragment
	u.Fragment = ""
	api := func(method, path, body string) []byte {
		t.Helper()
		request, _ := http.NewRequest(method, u.String()+path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode >= 400 {
			t.Fatalf("%s: HTTP %d %s", path, response.StatusCode, data)
		}
		return data
	}
	// The launcher has exited, but the new session still serves its configured API.
	if data := api("GET", "api/state", ""); !bytes.Contains(data, []byte(`"max_agents":4`)) {
		t.Fatalf("wrong child settings: %s", data)
	}
	if output, err := run("supervisor", "--detached", "--listen=127.0.0.1:0"); err == nil || !strings.Contains(output, "already running") {
		t.Fatalf("duplicate launch: %s %v", output, err)
	}
	var instance websupervisor.InstanceInfo
	if err := json.Unmarshal(api("POST", "api/instances", `{"mode":"shell","path":""}`), &instance); err != nil {
		t.Fatal(err)
	}
	endpoint := *u
	endpoint.Scheme = "ws"
	endpoint.Path = "/api/instances/" + instance.ID + "/terminal"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{Subprotocols: []string{"omo", "omo-token." + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("printf '\\nOWNED_SHELL:%s:END\\n' $$\n")); err != nil {
		t.Fatal(err)
	}
	var transcript strings.Builder
	for !strings.Contains(transcript.String(), ":END\r\n") {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		transcript.Write(data)
	}
	text := transcript.String()
	start := strings.LastIndex(text, "OWNED_SHELL:")
	pid, err := strconv.Atoi(strings.Split(text[start+len("OWNED_SHELL:"):], ":")[0])
	if err != nil {
		t.Fatalf("shell PID: %q %v", text, err)
	}
	if output, err := run("supervisor", "stop"); err != nil || !strings.Contains(output, "Supervisor stopped.") {
		t.Fatalf("stop: %s %v", output, err)
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("owned shell %d survived stop: %v", pid, err)
	}
	if output, err := run("supervisor", "stop"); err != nil || !strings.Contains(output, "not running") {
		t.Fatalf("idempotent stop: %s %v", output, err)
	}
	// A bind failure is returned synchronously and remains diagnosable in the log.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if output, err := run("supervisor", "-d", "--listen="+occupied.Addr().String()); err == nil || !strings.Contains(output, "failed to start") {
		t.Fatalf("bind failure: %s %v", output, err)
	}
	log, err := os.ReadFile(filepath.Join(dir, "supervisor.log"))
	if err != nil || !strings.Contains(string(log), "bind") {
		t.Fatalf("failure log: %s %v", log, err)
	}
	// Exercise the actual saved invocation without changing this user's login setup.
	args := []string{"supervisor", "--listen=127.0.0.1:0", "--max-agents=7", "--usage-cache-ttl=3m0s", "--mock=false", "--unsafe=false", "--no-origin-check=true", "--basic-auth=user:p a'ss\"$HOME`id`%&\\word"}
	home := filepath.Dir(dir)
	cwd, _ := os.Getwd()
	registration := Registration{Args: args, Directory: cwd, Home: home, Path: os.Getenv("PATH")}
	data, _ := json.Marshal(registration)
	config := filepath.Join(dir, "autostart.json")
	if err := writePrivate(config, data); err != nil {
		t.Fatal(err)
	}
	auto := exec.Command(binary, "supervisor", "autostart-run", config)
	if err := auto.Start(); err != nil {
		t.Fatal(err)
	}
	autoDone := make(chan error, 1)
	go func() { autoDone <- auto.Wait() }()
	t.Cleanup(func() {
		_ = auto.Process.Kill()
		select {
		case <-autoDone:
		case <-time.After(10 * time.Second):
			t.Error("autostart helper did not exit")
		}
	})
	state = awaitState(t, dir)
	request, _ := http.NewRequest("GET", state.URL+"api/state", nil)
	request.SetBasicAuth("user", "p a'ss\"$HOME`id`%&\\word")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Contains(data, []byte(`"max_agents":7`)) {
		t.Fatalf("autostart settings changed: HTTP %d %s", response.StatusCode, data)
	}
	if output, err := run("supervisor", "stop"); err != nil {
		t.Fatalf("autostart stop: %s %v", output, err)
	}
	select {
	case err := <-autoDone:
		if err != nil {
			t.Fatal(err)
		}
		autoDone <- nil
	case <-time.After(10 * time.Second):
		t.Fatal("autostart did not stop")
	}
}

func TestCancellationDuringStartupHookReleasesOwnership(t *testing.T) {
	dir := isolatedHome(t)
	plugin := filepath.Join(filepath.Dir(dir), "plugins", "startup")
	if err := os.MkdirAll(plugin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(dir), "hook-started")
	manifest := `{"name":"startup","hooks":[{"event":"supervisor_startup","lua":"start.lua"}]}`
	script := fmt.Sprintf(`omo.exec("sh", "-c", %q)`, "touch '"+marker+"'; exec sleep 60")
	for path, data := range map[string]string{
		filepath.Join(plugin, "plugin.json"): manifest, filepath.Join(plugin, "start.lua"): script,
		filepath.Join(filepath.Dir(dir), "config.yaml"): "trusted_offices: []\nplugins:\n  installed:\n    startup:\n      enabled: true\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, websupervisor.Options{Listen: "127.0.0.1:0", MaxAgents: 1}, io.Discard, "canceled")
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
wait:
	for {
		select {
		case <-deadline.C:
			t.Fatal("startup hook did not begin")
		case <-ticker.C:
			if _, err := os.Stat(marker); err == nil {
				break wait
			}
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("startup cancellation did not interrupt the hook")
	}
	if err := Stop(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("canceled startup retained ownership: %v", err)
	}
}
