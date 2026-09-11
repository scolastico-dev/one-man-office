package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/scolastico-dev/one-man-office/internal/filelock"
	"github.com/scolastico-dev/one-man-office/internal/pluginfiles"
)

func prepareDirectories(ctx context.Context, sources []Source) (entries []pluginDirectory, snapshot string, err error) {
	roots := map[string]bool{}
	for _, source := range sources {
		if !source.Shared {
			continue
		}
		if err := os.MkdirAll(source.Root, 0755); err != nil {
			return nil, "", err
		}
		root, err := filepath.EvalSymlinks(source.Root)
		if err != nil {
			return nil, "", err
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return nil, "", err
		}
		roots[root] = true
	}
	ordered := make([]string, 0, len(roots))
	for root := range roots {
		ordered = append(ordered, root)
	}
	sort.Strings(ordered)
	var locks []*filelock.Lock
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			_ = locks[i].Close()
		}
	}()
	for _, root := range ordered {
		lock, err := pluginfiles.Lock(ctx, root)
		if err != nil {
			return nil, "", fmt.Errorf("lock shared plugin source: %w", err)
		}
		locks = append(locks, lock)
	}
	entries, err = selectDirectories(sources)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if err != nil && snapshot != "" {
			_ = os.RemoveAll(snapshot)
		}
	}()
	for i := range entries {
		entry := &entries[i]
		if !entry.shared || entry.dir == "" || (entry.managed && !entry.settings.Enabled) {
			continue
		}
		if snapshot == "" {
			snapshot, err = os.MkdirTemp("", "omo-plugin-runtime-*")
			if err != nil {
				return nil, "", err
			}
		}
		dest := filepath.Join(snapshot, entry.name)
		if err = pluginfiles.CopyTree(entry.dir, dest); err != nil {
			return nil, snapshot, fmt.Errorf("snapshot plugin %s: %w", entry.name, err)
		}
		entry.dir = dest
	}
	return entries, snapshot, nil
}

// Close prevents new manual runs, cancels active hooks, waits until their
// outcome audits have been written, then releases private runtime snapshots.
// Callers must stop Run and other event producers before closing the manager.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.manualMu.Lock()
	m.manualClosing = true
	if m.manualCancel != nil {
		m.manualCancel()
	}
	m.manualMu.Unlock()
	m.manualWG.Wait()
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closed {
		return nil
	}
	_, _ = m.emitLifecycleUnlocked(context.Background(), EventUnload, map[string]any{}, true)
	m.closed = true
	var snapshotErr error
	if m.snapshotDir != "" {
		snapshotErr = os.RemoveAll(m.snapshotDir)
	}
	return snapshotErr
}
