package session

import (
	"fmt"
	"os"
	"sync"
)

// rotatingWriter caps a session transcript. A CEO lives as long as the office
// does, so without a bound its log grows without limit. On reaching maxBytes
// the live file becomes <name>.1 and older files shift up. keep < 0 retains
// every segment; otherwise it bounds segments for this one live session.
type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64 // 0 disables rotation
	keep     int
	f        *os.File
	size     int64
}

func newRotatingWriter(path string, maxSizeKB, keep int) (*rotatingWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	var size int64
	if fi, err := f.Stat(); err == nil {
		size = fi.Size()
	}
	return &rotatingWriter{
		path:     path,
		maxBytes: int64(maxSizeKB) * 1024,
		keep:     keep,
		f:        f,
		size:     size,
	}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return len(p), nil // closed: drop late output rather than error
	}
	if w.maxBytes > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate must be called with the lock held.
func (w *rotatingWriter) rotate() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	if w.keep == 0 {
		// No history wanted: start the live log over.
		os.Remove(w.path)
	} else {
		high := w.keep
		if high < 0 {
			high = 1
			for {
				if _, err := os.Stat(fmt.Sprintf("%s.%d", w.path, high)); os.IsNotExist(err) {
					break
				}
				high++
			}
		}
		if w.keep > 0 {
			os.Remove(fmt.Sprintf("%s.%d", w.path, w.keep))
		}
		for i := high - 1; i >= 1; i-- {
			os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
		}
		if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.f, w.size = f, 0
	return nil
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
