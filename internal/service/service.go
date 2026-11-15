// Package service manages the per-user omo server lifecycle: the run lock,
// detached start, authenticated stop and login autostart.
package service

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
	"sync"
	"sync/atomic"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/filelock"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

var ErrNotRunning = errors.New("omo is not running")

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
	return filepath.Join(home, "run"), nil
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

type hurryKey struct{}

// Hurry returns a channel that is closed when a stop request arrives after
// one that already started the stop, which means "do not wait any longer".
// The context is the one Run passes to its ServeFunc; otherwise the channel
// is nil and never closes.
func Hurry(ctx context.Context) <-chan struct{} {
	ch, _ := ctx.Value(hurryKey{}).(chan struct{})
	return ch
}

// ServeFunc runs the server until ctx ends. It calls ready with the access URL
// once the server accepts connections.
type ServeFunc func(ctx context.Context, out io.Writer, ready func(accessURL string) error) error

// Run owns one omo per OMO_HOME until serve returns.
// A nil output writes startup messages and errors to the private omo.log.
func Run(ctx context.Context, out io.Writer, launchID string, serve ServeFunc) (err error) {
	dir, err := prepareDirectory()
	if err != nil {
		return err
	}
	lock, err := shortLock(ctx, filepath.Join(dir, "run.lock"))
	if err != nil {
		return fmt.Errorf("omo is already running or starting (use omo stop): %w", err)
	}
	defer lock.Close()
	statePath := filepath.Join(dir, "runtime.json")
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	defer os.Remove(statePath)
	if out == nil {
		log, openErr := os.OpenFile(filepath.Join(dir, "omo.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if openErr != nil {
			return openErr
		}
		defer log.Close()
		out = log
		defer func() {
			if err != nil {
				fmt.Fprintln(out, "omo:", err)
			}
		}()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	hurry := make(chan struct{})
	ctx = context.WithValue(ctx, hurryKey{}, hurry)
	var stopping atomic.Bool
	var hurryOnce sync.Once
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
			if stopping.Swap(true) {
				hurryOnce.Do(func() { close(hurry) })
			}
			cancel()
		default:
			http.NotFound(w, r)
		}
	})
	defer control.Close()
	go func() { _ = control.Serve(listener) }()
	return serve(ctx, out, func(accessURL string) error {
		state.URL = accessURL
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		return writePrivate(statePath, data)
	})
}

// StartDetached waits for readiness; a failed launch never reports success.
// The child is the same executable started with only the hidden background id.
func StartDetached(ctx context.Context, executable string, out io.Writer) error {
	dir, err := prepareDirectory()
	if err != nil {
		return err
	}
	launchLock, err := shortLock(ctx, filepath.Join(dir, "launch.lock"))
	if err != nil {
		return fmt.Errorf("another omo launch is in progress: %w", err)
	}
	defer launchLock.Close()
	lock, err := shortLock(ctx, filepath.Join(dir, "run.lock"))
	if err != nil {
		return fmt.Errorf("omo is already running or starting (use omo stop): %w", err)
	}
	lock.Close()
	id, err := nonce()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "--background-id="+id)
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
			return fmt.Errorf("omo failed to start (%v); see %s", err, filepath.Join(dir, "omo.log"))
		case <-ctx.Done():
			interruptDetached(cmd.Process)
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				terminateDetached(cmd.Process)
				<-exited
			}
			return fmt.Errorf("omo startup canceled: %w; see %s", ctx.Err(), filepath.Join(dir, "omo.log"))
		case <-ticker.C:
			state, err := readState(dir)
			if err != nil || state.LaunchID != id {
				continue
			}
			if err := controlRequest(ctx, state, "GET", "/status"); err != nil {
				continue
			}
			fmt.Fprintln(out, "omo started in the background.")
			logPath := filepath.Join(dir, "omo.log")
			if startup, err := homefile.Read(logPath, homefile.MaxStartupLog); err == nil {
				// Preserve the same exposure/authentication warnings as foreground startup.
				fmt.Fprint(out, string(startup))
			} else {
				fmt.Fprintf(out, "omo: %s\n", state.URL)
			}
			fmt.Fprintf(out, "Log: %s\nStop with: omo stop\n", logPath)
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
		return fmt.Errorf("omo is starting or has no readable control state; retry shortly: %w", err)
	}
	if err := controlRequest(ctx, state, "POST", "/stop"); err != nil {
		return fmt.Errorf("request omo stop: %w", err)
	}
	lock, err := filelock.Acquire(ctx, lockPath)
	if err != nil {
		return fmt.Errorf("stop requested, but cleanup has not finished: %w", err)
	}
	return lock.Close()
}

// Running reports whether an omo owns the lifecycle lock and answers its
// control endpoint, and returns its access URL.
func Running(ctx context.Context) (accessURL string, running bool, err error) {
	dir, err := directory()
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	lock, err := shortLock(ctx, filepath.Join(dir, "run.lock"))
	if err == nil {
		lock.Close()
		return "", false, nil
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return "", false, err
	}
	state, err := readState(dir)
	if err != nil {
		return "", false, nil
	}
	if err := controlRequest(ctx, state, "GET", "/status"); err != nil {
		return "", false, nil
	}
	return state.URL, true, nil
}

func controlRequest(ctx context.Context, state runtimeState, method, path string) error {
	host, port, err := net.SplitHostPort(state.Endpoint)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || number < 1 || number > 65535 || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || state.Token == "" {
		return fmt.Errorf("invalid local omo control state")
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
	data, err := homefile.Read(filepath.Join(dir, "runtime.json"), homefile.MaxRunFile)
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
