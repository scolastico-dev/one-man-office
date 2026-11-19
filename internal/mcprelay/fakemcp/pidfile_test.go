package fakemcp

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRecordPIDLeavesOneFileNamedByTheProcessID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvPIDDir, dir)
	if err := recordPID(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, strconv.Itoa(os.Getpid()))); err != nil {
		t.Fatalf("no file for this process: %v", err)
	}
}

func TestRecordPIDReportsAnUnwritableFolderAndIgnoresNone(t *testing.T) {
	t.Setenv(EnvPIDDir, "")
	if err := recordPID(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPIDDir, filepath.Join(t.TempDir(), "missing"))
	if err := recordPID(); err == nil {
		t.Fatal("a missing folder was accepted")
	}
}
