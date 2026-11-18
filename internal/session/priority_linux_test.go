//go:build linux

package session

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// threadNiceValues returns the nice value of every thread of this process,
// sorted and without repeats. On Linux a nice value belongs to a thread.
func threadNiceValues() ([]int, error) {
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for _, task := range tasks {
		raw, err := os.ReadFile(filepath.Join("/proc/self/task", task.Name(), "stat"))
		if err != nil {
			continue // the thread ended meanwhile
		}
		// The fields after the command name, which ends at the last ')'.
		fields := strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+1:]))
		if len(fields) < 17 {
			return nil, fmt.Errorf("unexpected stat of thread %s", task.Name())
		}
		nice, err := strconv.Atoi(fields[16]) // field 19 of the file
		if err != nil {
			return nil, err
		}
		seen[nice] = true
	}
	var out []int
	for nice := range seen {
		out = append(out, nice)
	}
	sort.Ints(out)
	return out, nil
}

// An agent runs with a lowered priority in every one of its threads, also
// those its runtime created before omo could have looked at it, and omo
// itself keeps its own priority.
func TestLowerPrioritySetsAgentNiceValue(t *testing.T) {
	rawParentPriority, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	parentNice := 20 - rawParentPriority
	wantNice := min(parentNice+10, maxNiceValue)
	if wantNice == parentNice {
		t.Skipf("this process already runs at the highest nice value %d: nothing can be lowered", parentNice)
	}
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
	if screen := s.Screen(); !strings.Contains(screen, fmt.Sprintf("nice=[%d]", wantNice)) {
		t.Fatalf("agent priority not lowered in every thread, want nice=[%d]:\n%s", wantNice, strings.TrimSpace(screen))
	}
	// The thread that started the agent was lowered and then ended; no
	// thread of omo itself may be left with the lowered priority.
	deadline := time.Now().Add(10 * time.Second)
	for {
		own, err := threadNiceValues()
		if err != nil {
			t.Fatal(err)
		}
		if len(own) == 1 && own[0] == parentNice {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("threads of this process have the nice values %v, want only %d", own, parentNice)
		}
		time.Sleep(10 * time.Millisecond)
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
	values, err := threadNiceValues()
	if err != nil {
		fmt.Printf("priority error: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("nice=%v\n", values)
	os.Exit(0)
}
