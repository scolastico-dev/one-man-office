//go:build !windows

package sandbox

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCloseInheritedFDsIncludesDescriptorsAbove1023(t *testing.T) {
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD, 2048)
	if err != nil {
		t.Skipf("cannot allocate high descriptor: %v", err)
	}
	defer unix.Close(fd)
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
		t.Fatal(err)
	}
	if err := closeInheritedFDs(2048); err != nil {
		t.Fatal(err)
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("FD %d survives exec", fd)
	}
}
