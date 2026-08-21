//go:build linux

package session

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLowerPrioritySetsAgentNiceValue(t *testing.T) {
	rawParentPriority, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantNice := min(20-rawParentPriority+10, maxNiceValue)
	s, err := Start(Options{
		Cmd: os.Args[0], Args: []string{"-test.run=^TestPriorityHelperProcess$"},
		Env: []string{"OMO_PRIORITY_HELPER=1"}, LogPath: filepath.Join(t.TempDir(), "priority.log"),
		Rows: 24, Cols: 80, LowerPriority: true, NiceIncrement: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendLine("report"); err != nil {
		t.Fatal(err)
	}
	<-s.Done()
	if screen := s.Screen(); !strings.Contains(screen, fmt.Sprintf("nice=%d", wantNice)) {
		t.Fatalf("agent priority not lowered:\n%s", screen)
	}
}

func TestPriorityHelperProcess(t *testing.T) {
	if os.Getenv("OMO_PRIORITY_HELPER") != "1" {
		return
	}
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		fmt.Printf("read error: %v\n", err)
		os.Exit(2)
	}
	rawPriority, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if err != nil {
		fmt.Printf("priority error: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("nice=%d\n", 20-rawPriority)
	os.Exit(0)
}
