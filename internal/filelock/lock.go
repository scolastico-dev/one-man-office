// Package filelock provides context-aware advisory locks shared across omo
// processes. Lock files must stay in place; removing one breaks coordination.
package filelock

import (
	"context"
	"os"
	"sync"
	"time"
)

type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

func Acquire(ctx context.Context, path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		acquired, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if acquired {
			return &Lock{file: f}, nil
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *Lock) Close() error {
	l.once.Do(func() { unlock(l.file); l.err = l.file.Close() })
	return l.err
}
