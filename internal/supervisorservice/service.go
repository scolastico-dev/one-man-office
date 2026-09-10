// Package supervisorservice manages the browser supervisor's per-user lifecycle.
package supervisorservice

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/filelock"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/websupervisor"
)

var ErrNotRunning = errors.New("supervisor is not running")

const lockWait = 150 * time.Millisecond

type runtimeState struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
	URL      string `json:"url"`
	LaunchID string `json:"launch_id"`
}

func directory() (string, error) {
	home, err := globalhome.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "supervisor"), nil
}

func prepareDirectory() (string, error) {
	dir, err := directory()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	// This directory contains local control credentials and startup settings.
	if err := secureDirectory(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func shortLock(ctx context.Context, path string) (*filelock.Lock, error) {
	ctx, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()
	return filelock.Acquire(ctx, path)
}

// Run owns one supervisor per OMO_HOME until all owned terminals are cleaned up.
// A nil output writes startup messages and errors to the private supervisor.log.
func Run(ctx context.Context, options websupervisor.Options, out io.Writer, launchID string) (err error) {
	dir, err := prepareDirectory()
	if err != nil {
		return err
	}
	lock, err := shortLock(ctx, filepath.Join(dir, "run.lock"))
	if err != nil {
		return fmt.Errorf("supervisor is already running or starting (use omo supervisor stop): %w", err)
	}
	defer lock.Close()
	statePath := filepath.Join(dir, "runtime.json")
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	defer os.Remove(statePath)
	if out == nil {
		log, openErr := os.OpenFile(filepath.Join(dir, "supervisor.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if openErr != nil {
			return openErr
		}
		defer log.Close()
		out = log
		defer func() {
			if err != nil {
				fmt.Fprintln(out, "omo supervisor:", err)
			}
		}()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	token, err := nonce()
	if err != nil {
		return err
	}
	state := runtimeState{Endpoint: listener.Addr().String(), Token: token, LaunchID: launchID}
	control := &http.Server{ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 3 * time.Second}
	control.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != state.Endpoint || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "local control authentication required", http.StatusForbidden)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/status":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && r.URL.Path == "/stop":
			w.WriteHeader(http.StatusAccepted)
			cancel()
		default:
			http.NotFound(w, r)
		}
	})
	defer control.Close()
	go func() { _ = control.Serve(listener) }()
	return websupervisor.RunWithReady(ctx, options, out, func(accessURL string) error {
		state.URL = accessURL
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		return writePrivate(statePath, data)
	})
}

// StartDetached waits for readiness; a failed launch never reports success.
// args are literal supervisor flag arguments with no detachment flag.
func StartDetached(ctx context.Context, executable string, args []string, out io.Writer) error {
	dir, err := prepareDirectory()
	if err != nil {
		return err
	}
	launchLock, err := shortLock(ctx, filepath.Join(dir, "launch.lock"))
	if err != nil {
		return fmt.Errorf("another supervisor launch is in progress: %w", err)
	}
	defer launchLock.Close()
	lock, err := shortLock(ctx, filepath.Join(dir, "run.lock"))
	if err != nil {
		return fmt.Errorf("supervisor is already running or starting (use omo supervisor stop): %w", err)
	}
	lock.Close()
	id, err := nonce()
	if err != nil {
		return err
	}
	argv := append([]string{"supervisor"}, args...)
	argv = append(argv, "--background-id="+id)
	cmd := exec.Command(executable, argv...)
	configureDetached(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exited:
			return fmt.Errorf("supervisor failed to start (%v); see %s", err, filepath.Join(dir, "supervisor.log"))
		case <-ctx.Done():
			interruptDetached(cmd.Process)
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				terminateDetached(cmd.Process)
				<-exited
			}
			return fmt.Errorf("supervisor startup canceled: %w; see %s", ctx.Err(), filepath.Join(dir, "supervisor.log"))
		case <-ticker.C:
			state, err := readState(dir)
			if err != nil || state.LaunchID != id {
				continue
			}
			if err := controlRequest(ctx, state, "GET", "/status"); err != nil {
				continue
			}
			fmt.Fprintln(out, "Supervisor started in the background.")
			logPath := filepath.Join(dir, "supervisor.log")
			if startup, err := os.ReadFile(logPath); err == nil {
				// Preserve the same exposure/authentication warnings as foreground startup.
				fmt.Fprint(out, string(startup))
			} else {
				fmt.Fprintf(out, "omo supervisor: %s\n", state.URL)
			}
			fmt.Fprintf(out, "Log: %s\nStop with: omo supervisor stop\n", logPath)
			return nil
		}
	}
}

// Stop authenticates to the owner, then waits for its lifecycle lock to release.
// It never sends a signal to a PID read from disk, even when state is stale.
func Stop(ctx context.Context) error {
	dir, err := directory()
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return ErrNotRunning
	} else if err != nil {
		return err
	}
	lockPath := filepath.Join(dir, "run.lock")
	if lock, err := shortLock(ctx, lockPath); err == nil {
		defer lock.Close()
		_ = os.Remove(filepath.Join(dir, "runtime.json"))
		return ErrNotRunning
	} else if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	state, err := readState(dir)
	if err != nil {
		return fmt.Errorf("supervisor is starting or has no readable control state; retry shortly: %w", err)
	}
	if err := controlRequest(ctx, state, "POST", "/stop"); err != nil {
		return fmt.Errorf("request supervisor stop: %w", err)
	}
	lock, err := filelock.Acquire(ctx, lockPath)
	if err != nil {
		return fmt.Errorf("stop requested, but cleanup has not finished: %w", err)
	}
	return lock.Close()
}

func controlRequest(ctx context.Context, state runtimeState, method, path string) error {
	host, port, err := net.SplitHostPort(state.Endpoint)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || number < 1 || number > 65535 || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || state.Token == "" {
		return fmt.Errorf("invalid local supervisor control state")
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://"+state.Endpoint+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+state.Token)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("control request returned HTTP %d", response.StatusCode)
	}
	return nil
}

func readState(dir string) (runtimeState, error) {
	var state runtimeState
	data, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if err == nil {
		err = json.Unmarshal(data, &state)
	}
	return state, err
}

func nonce() (string, error) {
	var data [24]byte
	_, err := rand.Read(data[:])
	return hex.EncodeToString(data[:]), err
}

func writePrivate(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".omo-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return replaceFile(f.Name(), path)
}
