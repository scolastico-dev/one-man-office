package plugins

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This subprocess fixture holds an inherited output pipe after its parent is
// canceled, as a shell's descendant can do. The outer test always kills it.
func TestManualPipeHelper(t *testing.T) {
	if os.Getenv("OMO_PLUGIN_NAME") != "pipe-holder" {
		return
	}
	if os.Args[len(os.Args)-1] == "child" {
		if err := os.WriteFile("manual-child.pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(2)
		}
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestManualPipeHelper$", "--", "child")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Run(); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestManualShutdownBoundsInheritedOutputPipeWait(t *testing.T) {
	for _, kind := range []string{"command", "lua-exec"} {
		t.Run(kind, func(t *testing.T) {
			office, database := newPluginOffice(t)
			dir := filepath.Join(office, Dir, "pipe-holder")
			hook := Hook{Event: EventManual, Name: "run", Description: "Run action", Command: []string{os.Args[0], "-test.run=^TestManualPipeHelper$", "--", "parent"}}
			script := ""
			if kind == "lua-exec" {
				hook.Command = nil
				hook.Lua = "hook.lua"
				script = fmt.Sprintf(`local _, err = omo.exec(%q, "-test.run=^TestManualPipeHelper$", "--", "parent"); if err ~= "" then error(err) end`, filepath.ToSlash(os.Args[0]))
			}
			writePlugin(t, dir, Manifest{Name: "pipe-holder", Hooks: []Hook{hook}}, script)
			m, err := Load(office, database)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(m.Close)
			done := make(chan error, 1)
			go func() { done <- m.TriggerManual("pipe-holder", "run", "user", nil) }()
			var pidText []byte
			deadline := time.Now().Add(3 * time.Second)
			for {
				pidText, err = os.ReadFile(filepath.Join(dir, "manual-child.pid"))
				if err == nil && len(pidText) > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("descendant did not start")
				}
				time.Sleep(time.Millisecond)
			}
			pid, err := strconv.Atoi(string(pidText))
			if err != nil {
				t.Fatal(err)
			}
			child, err := os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = child.Kill() })
			closed := make(chan struct{})
			go func() { m.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Error("shutdown waited for the descendant's inherited output pipe")
				_ = child.Kill()
				<-closed
			}
			if err := <-done; err == nil || strings.Contains(err.Error(), "database is closed") {
				t.Fatalf("canceled manual hook result = %v", err)
			}
			var count int
			if err := database.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_failed'`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatal("missing cancellation audit")
			}
		})
	}
}
