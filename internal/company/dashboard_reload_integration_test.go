//go:build !windows

package company

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const browserDashboardAuth = "browser:secret"

type browserReloadTerminal struct {
	mu        sync.Mutex
	current   []byte
	output    chan []byte
	closed    chan struct{}
	exited    chan struct{}
	closeOnce sync.Once
	trigger   sync.Once
	onTrigger func()
}

func newBrowserReloadTerminal() *browserReloadTerminal {
	return &browserReloadTerminal{
		output: make(chan []byte, 4), closed: make(chan struct{}), exited: make(chan struct{}),
	}
}

func (p *browserReloadTerminal) Read(data []byte) (int, error) {
	for {
		p.mu.Lock()
		if len(p.current) > 0 {
			n := copy(data, p.current)
			p.current = p.current[n:]
			p.mu.Unlock()
			return n, nil
		}
		p.mu.Unlock()
		select {
		case chunk := <-p.output:
			p.mu.Lock()
			p.current = append(p.current[:0], chunk...)
			p.mu.Unlock()
		case <-p.closed:
			return 0, io.EOF
		}
	}
}

func (p *browserReloadTerminal) Write(data []byte) (int, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("OMO_BROWSER_REPLAY")) {
		p.trigger.Do(func() {
			if p.onTrigger != nil {
				p.onTrigger()
			}
		})
	}
	return len(data), nil
}

func (p *browserReloadTerminal) Resize(uint16, uint16) error { return nil }
func (p *browserReloadTerminal) Wait() error                 { <-p.exited; return nil }
func (p *browserReloadTerminal) Kill() error                 { return p.Close() }
func (p *browserReloadTerminal) Close() error {
	p.closeOnce.Do(func() {
		close(p.closed)
		close(p.exited)
	})
	return nil
}

func (p *browserReloadTerminal) publish(data []byte) {
	copyData := append([]byte(nil), data...)
	select {
	case p.output <- copyData:
	case <-p.closed:
	}
}

func TestBrowserDashboardReloadReconnect(t *testing.T) {
	for _, variant := range []string{"current", "stale"} {
		t.Run(variant, func(t *testing.T) {
			dir := projectHome(t)
			if variant == "stale" {
				installStaleFilebrowser(t, filepath.Join(dir, "global"))
			}
			s, err := New(Options{MaxAgents: 2, BasicAuth: browserDashboardAuth})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			ts := httptest.NewUnstartedServer(s.Handler())
			t.Cleanup(ts.Close)
			s.authority = ts.Listener.Addr().String()
			ts.Start()

			terminal := newBrowserReloadTerminal()
			t.Cleanup(func() { _ = terminal.Close() })
			instance := ownInstance("browser-reload", filepath.Join(dir, "office"), "shell", terminal, nil)
			startup := []byte("\x1b[?1002h\x1b[?1006h\x1b[?1004h\x1b[?1049h\x1b[?2004hREADY\r\n")
			overflow := bytes.Repeat([]byte("replay-overflow\r\n"), 20000)
			terminal.onTrigger = func() { terminal.publish(overflow) }
			s.mu.Lock()
			s.instances[instance.info.ID] = instance
			s.mu.Unlock()
			terminal.publish(startup)
			waitForBrowserReplay(t, instance, len(startup))

			runBrowserNodeTestWithEnv(t, "dashboard_reload.test.cjs", "dashboard reload regression", "OMO_BROWSER_URL="+ts.URL+"/", "OMO_BROWSER_AUTH="+browserDashboardAuth, "OMO_BROWSER_VARIANT="+variant)
		})
	}
}

func waitForBrowserReplay(t *testing.T, instance *Instance, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		instance.mu.Lock()
		got := len(instance.replay)
		instance.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("terminal replay did not reach %d bytes", want)
}

func installStaleFilebrowser(t *testing.T, globalRoot string) {
	t.Helper()
	pluginRoot := filepath.Join(globalRoot, "plugins", "filebrowser")
	if err := os.MkdirAll(pluginRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	config := "trusted_offices: []\ntemplate:\n  enabled: false\n  auto_sync: false\n  setup_never_ask: false\nplugins:\n  update_on_start: false\n  installed:\n    filebrowser:\n      source: stale:test\n      enabled: true\n"
	if err := os.WriteFile(filepath.Join(globalRoot, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locating repository root for stale plugin failed")
	}
	archive := exec.Command("git", "archive", "--format=tar", "bbef593^", "plugins/filebrowser")
	archive.Dir = filepath.Clean(filepath.Join(filepath.Dir(filename), "../.."))
	data, err := archive.Output()
	if err != nil {
		t.Skipf("stale plugin revision is unavailable: %v", err)
	}
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		relative := strings.TrimPrefix(header.Name, "plugins/filebrowser/")
		if relative == "" || header.Typeflag != tar.TypeReg {
			continue
		}
		destination := filepath.Join(pluginRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatal(copyErr, closeErr)
		}
	}
	if _, err := os.Stat(filepath.Join(pluginRoot, "web", "main.js")); err != nil {
		t.Fatal(fmt.Errorf("stale filebrowser was not materialized: %w", err))
	}
}
