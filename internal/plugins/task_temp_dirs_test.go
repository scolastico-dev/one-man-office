package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync/atomic"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type testTaskTempDir struct{ ID, Path string }

func tempDirPlugin(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	for _, name := range []string{"p", "q"} {
		e.plugin(name, manifestOf(map[string]any{"event": "manual", "name": "allocate", "description": "Allocate", "lua": "allocate.lua"}), map[string]string{"allocate.lua": `local dir,err=omo.task_temp_dir(); assert(dir,err); return omo.json_encode(dir)`})
	}
	if errs := e.load(enabled("p", nil), enabled("q", nil)); len(errs) != 0 {
		t.Fatal(errs)
	}
	return e
}
func newTestTaskTempDir(t *testing.T, e *env, name string) testTaskTempDir {
	t.Helper()
	text, err := e.m.Trigger(t.Context(), name, "allocate", nil)
	if err != nil {
		t.Fatal(err)
	}
	var dir testTaskTempDir
	if err := json.Unmarshal([]byte(text), &dir); err != nil || dir.ID == "" || dir.Path == "" {
		t.Fatalf("lease result: %q, %v", text, err)
	}
	return dir
}
func taskWithTempDir(t *testing.T, req TaskStart, id string) TaskStart {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["temp_dir"] = id
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	return req
}
func tempDirExists(t *testing.T, path string) {
	t.Helper()
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("temporary directory missing: %s, %v", path, err)
	}
}
func tempDirRemoved(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary directory retained: %s, %v", path, err)
	}
}

// A long inherited OS temporary root must not produce an unusable IPC path.
func TestTaskTempDirShortPrivateAndOwned(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("ä", 80))
	if err := os.MkdirAll(long, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", long)
	t.Setenv("TMP", long)
	t.Setenv("TEMP", long)
	e := tempDirPlugin(t)
	a := newTestTaskTempDir(t, e, "p")
	b := newTestTaskTempDir(t, e, "q")
	if a.ID == b.ID || a.Path == b.Path {
		t.Fatal("temporary directories collided")
	}
	info, err := os.Stat(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0700 {
			t.Fatalf("mode = %v", info.Mode())
		}
		if len(a.Path) > 32 || strings.HasPrefix(a.Path, long) {
			t.Fatalf("temporary path is not short: %d bytes %s", len(a.Path), a.Path)
		}
	}
	task, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, taskRequest(t, "editor", "sleep"), a.ID))
	if err != nil {
		t.Fatal(err)
	}
	tempDirExists(t, a.Path)
	if err := e.m.StopTaskID("p", task.ID, TaskStopped); err != nil {
		t.Fatal(err)
	}
	tempDirRemoved(t, a.Path)
	tempDirExists(t, b.Path)
	e.m.Close()
	tempDirRemoved(t, b.Path)
}

// Every process termination route must release its lease before notifying consumers.
func TestTaskTempDirLifecycleCleanup(t *testing.T) {
	for _, mode := range []string{"natural", "explicit", "id", "idle", "forward_removed", "ticket", "disabled", "removed", "replaced", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			e := tempDirPlugin(t)
			lease := newTestTaskTempDir(t, e, "p")
			unused := newTestTaskTempDir(t, e, "p")
			other := newTestTaskTempDir(t, e, "q")
			notified := make(chan struct{}, 1)
			e.m.o.TaskExited = func(_ string, _ api.PluginTask) {
				if _, err := os.Lstat(lease.Path); !os.IsNotExist(err) {
					t.Errorf("temporary directory retained at callback: %v", err)
				}
				notified <- struct{}{}
			}
			req := taskRequest(t, "worker", "sleep")
			if mode == "natural" {
				req = taskRequest(t, "worker", "print", "done")
			}
			if mode == "ticket" {
				req.Ticket = 7
				e.m.o.TaskTicketLive = func(context.Context, int) bool { return true }
			}
			task, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, req, lease.ID))
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "natural":
				select {
				case <-notified:
				case <-time.After(3 * time.Second):
					t.Fatal("natural exit did not notify")
				}
			case "explicit":
				err = e.m.StopTask("p", "worker")
			case "id":
				err = e.m.StopTaskID("p", task.ID, TaskStopped)
			case "idle":
				err = e.m.StopTaskID("p", task.ID, "idle")
			case "forward_removed":
				err = e.m.StopTaskID("p", task.ID, TaskForwardRemoved)
			case "ticket":
				e.m.StopTicketTasks(7)
			case "disabled":
				s := enabled("p", nil)
				s.Enabled = false
				e.load(s, enabled("q", nil))
			case "removed":
				e.load(enabled("q", nil))
			case "replaced":
				if err := os.WriteFile(filepath.Join(e.root, "p", "allocate.lua"), []byte(`return "new code"`), 0600); err != nil {
					t.Fatal(err)
				}
				e.load(enabled("p", nil), enabled("q", nil))
			case "shutdown":
				e.m.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			tempDirRemoved(t, lease.Path)
			switch mode {
			case "disabled", "removed", "replaced", "shutdown":
				tempDirRemoved(t, unused.Path)
			default:
				tempDirExists(t, unused.Path)
			}
			if mode != "shutdown" {
				tempDirExists(t, other.Path)
			} else {
				tempDirRemoved(t, other.Path)
			}
		})
	}
}

func TestTaskTempDirFailedStartsReleaseClaimedLease(t *testing.T) {
	for _, mode := range []string{"validation", "spawn", "duplicate", "ticket", "ticket-race", "cancelled", "lua-decode"} {
		t.Run(mode, func(t *testing.T) {
			e := tempDirPlugin(t)
			lease := newTestTaskTempDir(t, e, "p")
			req := taskRequest(t, "worker", "sleep")
			ctx := t.Context()
			switch mode {
			case "validation":
				req.Name = "../bad"
			case "spawn":
				req.Command = "/no/such/task/program"
			case "duplicate":
				if _, err := e.m.StartTask(ctx, "p", req); err != nil {
					t.Fatal(err)
				}
			case "ticket":
				req.Ticket = 7
			case "ticket-race":
				var calls atomic.Int32
				req.Ticket = 7
				e.m.o.TaskTicketLive = func(context.Context, int) bool { return calls.Add(1) == 1 }
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "lua-decode":
				source := `local dir=assert(omo.task_temp_dir()); omo.store_set("dir",dir.path); local task,err=omo.task_start{name=123,command="missing",temp_dir=dir.id}; assert(not task and err); return dir.path`
				e.plugin("p", manifestOf(map[string]any{"event": "manual", "name": "bad", "description": "Bad", "lua": "bad.lua"}), map[string]string{"bad.lua": source})
				e.load(enabled("p", nil), enabled("q", nil))
				path, err := e.m.Trigger(ctx, "p", "bad", nil)
				if err != nil {
					t.Fatal(err)
				}
				tempDirRemoved(t, path)
				return
			}
			_, err := e.m.StartTask(ctx, "p", taskWithTempDir(t, req, lease.ID))
			if err == nil {
				t.Fatal("invalid start succeeded")
			}
			tempDirRemoved(t, lease.Path)
		})
	}
}

// Claims are runtime-owned, single-use opaque IDs; names cannot retarget cleanup.
func TestTaskTempDirOwnershipAndStableTaskIDs(t *testing.T) {
	e := tempDirPlugin(t)
	a := newTestTaskTempDir(t, e, "p")
	b := newTestTaskTempDir(t, e, "p")
	req := taskRequest(t, "worker", "sleep")
	for _, id := range []string{a.ID, "../arbitrary", a.Path} {
		_, err := e.m.StartTask(t.Context(), "q", taskWithTempDir(t, req, id))
		if err == nil {
			t.Fatalf("wrong-owner or malformed lease accepted: %q", id)
		}
		tempDirExists(t, a.Path)
		tempDirExists(t, b.Path)
	}
	first, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, req, a.ID))
	if err != nil {
		t.Fatal(err)
	}
	secondReq := taskRequest(t, "second", "sleep")
	if _, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, secondReq, a.ID)); err == nil {
		t.Fatal("consumed lease reused")
	}
	tempDirExists(t, a.Path)
	second, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, secondReq, b.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.StopTaskID("p", first.ID, TaskStopped); err != nil {
		t.Fatal(err)
	}
	tempDirRemoved(t, a.Path)
	tempDirExists(t, b.Path)
	c := newTestTaskTempDir(t, e, "p")
	replacement, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, req, c.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.StopTaskID("p", first.ID, TaskStopped); err != nil {
		t.Fatal(err)
	}
	tempDirExists(t, c.Path)
	tempDirExists(t, b.Path)
	if err := e.m.StopTaskID("p", replacement.ID, TaskStopped); err != nil {
		t.Fatal(err)
	}
	tempDirRemoved(t, c.Path)
	tempDirExists(t, b.Path)
	if err := e.m.StopTaskID("p", second.ID, TaskStopped); err != nil {
		t.Fatal(err)
	}
	tempDirRemoved(t, b.Path)
}

func TestTaskTempDirUnattachedBoundsExpiryAndRuntimeEnd(t *testing.T) {
	e := tempDirPlugin(t)
	clock := &fakeClock{}
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	e.m.now = func() time.Time { return time.Unix(0, now.Load()) }
	e.m.after = clock.after
	var leases []testTaskTempDir
	for range 16 {
		leases = append(leases, newTestTaskTempDir(t, e, "p"))
	}
	if _, err := e.m.Trigger(t.Context(), "p", "allocate", nil); err == nil || !strings.Contains(err.Error(), "16") {
		t.Fatalf("unattached allocation limit: %v", err)
	}
	now.Add(int64(61 * time.Second))
	clock.fire(e.m.now())
	for _, lease := range leases {
		eventually(t, "temporary lease expiry", func() bool { _, err := os.Lstat(lease.Path); return os.IsNotExist(err) })
	}
	if _, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, taskRequest(t, "stale", "sleep"), leases[0].ID)); err == nil {
		t.Fatal("expired lease accepted")
	}
	own := newTestTaskTempDir(t, e, "p")
	other := newTestTaskTempDir(t, e, "q")
	// A settings-only reload replaces the runtime but retains running tasks.
	task, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, taskRequest(t, "running", "sleep"), own.ID))
	if err != nil {
		t.Fatal(err)
	}
	unclaimed := newTestTaskTempDir(t, e, "p")
	e.load(enabled("p", map[string]any{"new": true}), enabled("q", nil))
	tempDirRemoved(t, unclaimed.Path)
	if _, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, taskRequest(t, "old-lease", "sleep"), unclaimed.ID)); err == nil {
		t.Fatal("replacement runtime accepted an old allocation")
	}
	tempDirExists(t, own.Path)
	tempDirExists(t, other.Path)
	if err := e.m.StopTaskID("p", task.ID, TaskStopped); err != nil {
		t.Fatal(err)
	}
	tempDirRemoved(t, own.Path)
	e.m.Close()
	tempDirRemoved(t, other.Path)
}

func TestTaskTempDirCleanupDoesNotFollowSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary")
	}
	e := tempDirPlugin(t)
	lease := newTestTaskTempDir(t, e, "p")
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(lease.Path, "user-files")); err != nil {
		t.Fatal(err)
	}
	task, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, taskRequest(t, "worker", "sleep"), lease.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.StopTaskID("p", task.ID, TaskStopped); err != nil {
		t.Fatal(err)
	}
	tempDirRemoved(t, lease.Path)
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("cleanup touched outside files: %q %v", got, err)
	}
}

func TestTaskTempDirExpiryBeforeTimerAndAttachedLifetime(t *testing.T) {
	e := tempDirPlugin(t)
	clock := &fakeClock{}
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	e.m.now = func() time.Time { return time.Unix(0, now.Load()) }
	e.m.after = clock.after
	attached := newTestTaskTempDir(t, e, "p")
	expired := newTestTaskTempDir(t, e, "p")
	task, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, taskRequest(t, "running", "sleep"), attached.ID))
	if err != nil {
		t.Fatal(err)
	}
	now.Add(int64(61 * time.Second))
	// Claim must check the clock even when the expiry callback has not run.
	if _, err := e.m.StartTask(t.Context(), "p", taskWithTempDir(t, taskRequest(t, "expired", "sleep"), expired.ID)); err == nil {
		t.Fatal("expired lease started before timer dispatch")
	}
	tempDirRemoved(t, expired.Path)
	clock.fire(e.m.now())
	e.m.taskTempDirsWG.Wait()
	tempDirExists(t, attached.Path)
	if err := e.m.StopTaskID("p", task.ID, TaskStopped); err != nil {
		t.Fatal(err)
	}
	tempDirRemoved(t, attached.Path)
}

func TestTaskTempDirCleanupDoesNotFollowReplacedAllocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary")
	}
	e := tempDirPlugin(t)
	lease := newTestTaskTempDir(t, e, "p")
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	// Move the empty allocation into the test's own cleanup tree and replace its
	// old name with a link. The allocator must unlink rather than follow it.
	if err := os.Rename(lease.Path, filepath.Join(t.TempDir(), "allocation")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, lease.Path); err != nil {
		t.Fatal(err)
	}
	e.m.Close()
	tempDirRemoved(t, lease.Path)
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("cleanup touched replaced directory target: %q %v", got, err)
	}
}

// Faults wrap the real confined root rather than replacing filesystem effects.
type taskTempFaultRoot struct {
	root     taskTempDirRoot
	failures int
	err      error
	names    []string
	closes   int
}

func (r *taskTempFaultRoot) RemoveAll(name string) error {
	r.names = append(r.names, name)
	if len(r.names) <= r.failures {
		return r.err
	}
	return r.root.RemoveAll(name)
}
func (r *taskTempFaultRoot) Close() error { r.closes++; return r.root.Close() }
func faultTaskTempRoot(t *testing.T, e *env, lease testTaskTempDir, failures int, err error) (*taskTempDir, *taskTempFaultRoot) {
	t.Helper()
	e.m.tasksMu.Lock()
	defer e.m.tasksMu.Unlock()
	dir := e.m.taskTempDirs[lease.ID]
	if dir == nil {
		t.Fatal("missing allocation")
	}
	fault := &taskTempFaultRoot{root: dir.root, failures: failures, err: err}
	dir.root = fault
	return dir, fault
}

// An exiting process tree can briefly retain handles even after its parent is reaped.
func TestTaskTempDirCleanupRetriesTransientFailure(t *testing.T) {
	e := tempDirPlugin(t)
	lease := newTestTaskTempDir(t, e, "p")
	t.Cleanup(func() { _ = os.RemoveAll(lease.Path) })
	var logs bytes.Buffer
	e.m.o.Log = log.New(&logs, "", 0)
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(lease.Path, "link")); err != nil {
			t.Fatal(err)
		}
	}
	dir, fault := faultTaskTempRoot(t, e, lease, 2, errors.New("temporary sharing violation"))
	e.m.discardTaskTempDir(dir.owner, lease.ID)
	tempDirRemoved(t, lease.Path)
	if len(fault.names) != 3 || fault.closes != 1 {
		t.Fatalf("removals=%d closes=%d", len(fault.names), fault.closes)
	}
	for _, name := range fault.names {
		if name != filepath.Base(lease.Path) {
			t.Fatalf("cleanup changed its owned name: %q", name)
		}
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("cleanup touched outside file: %q %v", got, err)
	}
	if logs.Len() != 0 {
		t.Fatalf("transient failure was logged as permanent: %s", logs.String())
	}
	e.m.removeTaskTempDir(dir)
	if len(fault.names) != 3 || fault.closes != 1 {
		t.Fatal("duplicate cleanup repeated filesystem operations")
	}
}

func TestTaskTempDirCleanupPersistentFailureIsBounded(t *testing.T) {
	e := tempDirPlugin(t)
	lease := newTestTaskTempDir(t, e, "p")
	t.Cleanup(func() { _ = os.RemoveAll(lease.Path) })
	var logs bytes.Buffer
	e.m.o.Log = log.New(&logs, "", 0)
	dir, fault := faultTaskTempRoot(t, e, lease, 1000, errors.New("persistent sharing violation"))
	done := make(chan struct{})
	go func() { e.m.discardTaskTempDir(dir.owner, lease.ID); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("temporary directory cleanup retried without a finite bound")
	}
	tempDirExists(t, lease.Path)
	if len(fault.names) != 11 || fault.closes != 1 {
		t.Fatalf("removals=%d closes=%d", len(fault.names), fault.closes)
	}
	if strings.Count(logs.String(), "could not be removed") != 1 {
		t.Fatalf("persistent failure log: %s", logs.String())
	}
	e.m.removeTaskTempDir(dir)
	if len(fault.names) != 11 || fault.closes != 1 {
		t.Fatal("duplicate failed cleanup repeated filesystem operations")
	}
}

func TestTaskTempDirCleanupMissingDirectoryIsSuccess(t *testing.T) {
	e := tempDirPlugin(t)
	lease := newTestTaskTempDir(t, e, "p")
	if err := os.RemoveAll(lease.Path); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	e.m.o.Log = log.New(&logs, "", 0)
	dir, fault := faultTaskTempRoot(t, e, lease, 1, os.ErrNotExist)
	e.m.discardTaskTempDir(dir.owner, lease.ID)
	if len(fault.names) != 1 || fault.closes != 1 {
		t.Fatalf("missing directory retried: removals=%d closes=%d", len(fault.names), fault.closes)
	}
	if logs.Len() != 0 {
		t.Fatalf("missing directory logged as a cleanup failure: %s", logs.String())
	}
}
