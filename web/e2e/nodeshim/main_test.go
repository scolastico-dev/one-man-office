package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveReadsTheTargetBesideTheShim(t *testing.T) {
	dir := t.TempDir()
	want, err := filepath.Abs(filepath.Join(dir, "node"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, TargetFile), []byte(want+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolve(dir)
	if err != nil || got != want {
		t.Fatalf("resolve = %q, %v; want %q", got, err, want)
	}
}

func TestResolveRefusesAMissingOrRelativeTarget(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolve(dir); err == nil {
		t.Fatal("a missing target file was accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, TargetFile), []byte("node"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(dir); err == nil {
		t.Fatal("a relative target was accepted")
	}
}
