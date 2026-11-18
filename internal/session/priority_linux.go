//go:build linux

package session

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

const maxNiceValue = 19

// startWithLoweredPriority runs start, which starts a program, so that the
// program has a nice value raised by increment from its first instruction.
//
// A program inherits the nice value of the thread that starts it. On Linux a
// nice value belongs to a single thread, and changing it for a process id
// reaches only that process's first thread: lowering a program after it was
// started leaves every thread it had already created at the old priority.
// So the starting thread is lowered first. An unprivileged process cannot
// raise its priority again, therefore the start runs on a thread of its own
// that is never handed back to the Go runtime: the runtime ends a locked
// thread when its goroutine returns, and omo itself keeps its priority.
func startWithLoweredPriority(increment int, start func() error) error {
	done := make(chan error, 1)
	go lowerThenStart(increment, start, done)
	return <-done
}

// lowerThenStart is the goroutine of startWithLoweredPriority. The process's
// first thread is the one thread the runtime cannot end, so it must never be
// lowered: a goroutine that finds itself on it keeps it locked, which keeps
// every other goroutine off it, and hands the work to the next goroutine.
func lowerThenStart(increment int, start func() error, done chan<- error) {
	runtime.LockOSThread()
	if unix.Gettid() == unix.Getpid() {
		next := make(chan error, 1)
		go lowerThenStart(increment, start, next)
		err := <-next
		runtime.UnlockOSThread() // unchanged: it may serve the runtime again
		done <- err
		return
	}
	// Never unlocked: this thread ends with this goroutine.
	if err := lowerThreadPriority(increment); err != nil {
		done <- fmt.Errorf("lower process priority: %w", err)
		return
	}
	done <- start()
}

// lowerThreadPriority raises the calling thread's nice value by increment,
// up to the highest nice value.
func lowerThreadPriority(increment int) error {
	tid := unix.Gettid()
	rawPriority, err := unix.Getpriority(unix.PRIO_PROCESS, tid)
	if err != nil {
		return err
	}
	// Linux's raw getpriority result is 20 minus the user-visible nice value.
	currentNice := 20 - rawPriority
	targetNice := min(currentNice+increment, maxNiceValue)
	if targetNice <= currentNice {
		return nil
	}
	return unix.Setpriority(unix.PRIO_PROCESS, tid, targetNice)
}
