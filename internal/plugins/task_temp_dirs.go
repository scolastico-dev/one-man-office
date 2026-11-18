package plugins

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const (
	maxUnattachedTaskTempDirs = 16
	taskTempDirTTL            = time.Minute
)

var errTaskTempDir = errors.New("task temporary directory is unknown, expired or belongs to another plugin runtime")

type taskTempDirInfo struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type taskTempDirRoot interface {
	RemoveAll(string) error
	Close() error
}

// Only this allocator creates leases. A held parent root confines deletion even
// if a task replaces its temporary directory or inserts links within it.
type taskTempDir struct {
	info     taskTempDirInfo
	owner    *plugin
	expires  time.Time
	root     taskTempDirRoot
	name     string
	detached chan struct{}
	once     sync.Once
}

func (m *Manager) removeTaskTempDir(dir *taskTempDir) {
	if dir == nil {
		return
	}
	dir.once.Do(func() {
		defer dir.root.Close()
		// Process-group termination can precede descendant handle release.
		// Keep the same confined root/name and allow at most one second for it.
		for attempt := 0; attempt <= 10; attempt++ {
			err := dir.root.RemoveAll(dir.name)
			if err == nil || errors.Is(err, os.ErrNotExist) {
				return
			}
			if attempt == 10 {
				m.logf("plugins: a task temporary directory could not be removed")
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
}

func (m *Manager) allocateTaskTempDir(ctx context.Context, p *plugin) (taskTempDirInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || ctx.Err() != nil {
		return taskTempDirInfo{}, ErrClosed
	}
	if p.ctx.Err() != nil {
		return taskTempDirInfo{}, ErrUnknownPlugin
	}
	m.tasksMu.Lock()
	defer m.tasksMu.Unlock()
	count := 0
	for id, dir := range m.taskTempDirs {
		if dir.owner != p {
			continue
		}
		if !m.now().Before(dir.expires) {
			delete(m.taskTempDirs, id)
			close(dir.detached)
			m.removeTaskTempDir(dir)
		} else {
			count++
		}
	}
	if count >= maxUnattachedTaskTempDirs {
		return taskTempDirInfo{}, errors.New("the plugin runtime already has 16 unattached task temporary directories")
	}
	// Unix's inherited TMPDIR can be arbitrarily long. Keep generic task IPC and
	// runtime paths short without changing any process's inherited environment.
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = os.TempDir()
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return taskTempDirInfo{}, errors.New("the task temporary directory could not be created")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return taskTempDirInfo{}, errors.New("the task temporary directory could not be created")
	}
	name := ""
	for range 10 {
		candidate := "omo-" + rand.Text()[:12]
		if err = root.Mkdir(candidate, 0700); err == nil {
			name = candidate
			break
		}
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if name == "" {
		_ = root.Close()
		return taskTempDirInfo{}, errors.New("the task temporary directory could not be created")
	}
	dir := &taskTempDir{info: taskTempDirInfo{ID: rand.Text(), Path: filepath.Join(base, name)}, owner: p, expires: m.now().Add(taskTempDirTTL), root: root, name: name, detached: make(chan struct{})}
	if m.taskTempDirs == nil {
		m.taskTempDirs = map[string]*taskTempDir{}
	}
	m.taskTempDirs[dir.info.ID] = dir
	expiry := m.after(taskTempDirTTL)
	m.taskTempDirsWG.Add(1)
	go func() {
		defer m.taskTempDirsWG.Done()
		select {
		case <-expiry:
		case <-p.ctx.Done():
		case <-dir.detached:
			return
		}
		m.discardTaskTempDir(p, dir.info.ID)
	}()
	return dir.info, nil
}

// Claim detaches a lease from its runtime lifetime. The caller now owns cleanup
// on every failed start, or transfers it to one stable task identity on success.
func (m *Manager) claimTaskTempDir(p *plugin, id string) (*taskTempDir, error) {
	if id == "" {
		return nil, nil
	}
	m.tasksMu.Lock()
	defer m.tasksMu.Unlock()
	dir := m.taskTempDirs[id]
	if dir == nil || dir.owner != p {
		return nil, errTaskTempDir
	}
	delete(m.taskTempDirs, id)
	close(dir.detached)
	if !m.now().Before(dir.expires) {
		m.removeTaskTempDir(dir)
		return nil, errTaskTempDir
	}
	return dir, nil
}

// Invalid or foreign identifiers never select anything for removal.
func (m *Manager) discardTaskTempDir(p *plugin, id string) {
	m.tasksMu.Lock()
	defer m.tasksMu.Unlock()
	dir := m.taskTempDirs[id]
	if dir != nil && dir.owner == p {
		delete(m.taskTempDirs, id)
		close(dir.detached)
		m.removeTaskTempDir(dir)
	}
}
func (m *Manager) releaseTaskTempDirs(p *plugin) {
	m.tasksMu.Lock()
	defer m.tasksMu.Unlock()
	for id, dir := range m.taskTempDirs {
		if p == nil || dir.owner == p {
			delete(m.taskTempDirs, id)
			close(dir.detached)
			m.removeTaskTempDir(dir)
		}
	}
}
func (m *Manager) luaTaskTempDir(p *plugin) lua.LGFunction {
	return func(L *lua.LState) int {
		dir, err := m.allocateTaskTempDir(L.Context(), p)
		if err != nil {
			return fail(L, err.Error())
		}
		return pushTaskValue(L, dir)
	}
}
