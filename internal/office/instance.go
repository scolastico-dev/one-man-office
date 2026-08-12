package office

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

// LockPath records the transport endpoint owned by the running office.
const LockPath = ".omo/omo.lock"

const (
	instanceProbeTimeout = 250 * time.Millisecond
	instanceStartGrace   = 2 * time.Second
)

// AlreadyRunningError identifies the live endpoint found in the office lock.
type AlreadyRunningError struct {
	Endpoint string
}

func (e *AlreadyRunningError) Error() string {
	return fmt.Sprintf("omo is already running for this office at %s", e.Endpoint)
}

type instanceLock struct {
	path string
	file *os.File
}

func claimInstance(officeDir string) (*instanceLock, error) {
	path := filepath.Join(officeDir, LockPath)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			return &instanceLock{path: path, file: f}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create instance lock: %w", err)
		}
		endpoint, running, err := inspectInstance(path, true)
		if err != nil {
			return nil, err
		}
		if running {
			return nil, &AlreadyRunningError{Endpoint: endpoint}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove stale instance lock: %w", err)
		}
	}
}

func inspectInstance(path string, allowStarting bool) (endpoint string, running bool, err error) {
	deadline := time.Now().Add(instanceStartGrace)
	for {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				return "", false, nil
			}
			return "", false, fmt.Errorf("read instance lock: %w", readErr)
		}
		endpoint = strings.TrimSpace(string(raw))
		if endpoint != "" && sockc.Probe(endpoint, instanceProbeTimeout) {
			return endpoint, true, nil
		}
		if !allowStarting || time.Now().After(deadline) {
			return endpoint, false, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (l *instanceLock) setEndpoint(endpoint string) error {
	if err := l.file.Truncate(0); err != nil {
		return err
	}
	if _, err := l.file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := l.file.WriteString(endpoint + "\n"); err != nil {
		return err
	}
	return l.file.Sync()
}

func (l *instanceLock) release() {
	owned, currentErr := l.file.Stat()
	current, pathErr := os.Stat(l.path)
	_ = l.file.Close()
	if currentErr == nil && pathErr == nil && os.SameFile(owned, current) {
		_ = os.Remove(l.path)
	}
}

// Running returns the endpoint of a live office. A lock whose endpoint no
// longer accepts connections is stale and is removed.
func Running(officeDir string) (string, bool, error) {
	path := filepath.Join(officeDir, LockPath)
	endpoint, running, err := inspectInstance(path, false)
	if err != nil || running {
		return endpoint, running, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("remove stale instance lock: %w", err)
	}
	return "", false, nil
}
