package testguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTimingGuardRecognizesUnsafeCeilings(t *testing.T) {
	for _, expression := range []string{
		`if time.Since(start) > 100*time.Millisecond { t.Fatal("slow") }`,
		`elapsed := time.Since(start); if elapsed >= 900*time.Millisecond { t.Fatal("slow") }`,
		`if took := time.Now().Sub(start); time.Second < took { t.Fatal("slow") }`,
		`limit := 2*time.Second; if time.Since(start) > limit { t.Fatal("slow") }`,
	} {
		t.Run(expression, func(t *testing.T) {
			src := `package sample; import "time"; func TestCase(t *testing.T) { start := time.Now(); ` + expression + ` }`
			problems, err := timingProblems([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != 1 {
				t.Fatalf("got %v, want one unsafe elapsed ceiling", problems)
			}
		})
	}
}

func TestTimingGuardAllowsBehavioralAndDiagnosticTime(t *testing.T) {
	for _, expression := range []string{
		`if time.Since(start) < 50*time.Millisecond { t.Fatal("too short") }`,
		`if time.Since(start) > time.Minute { t.Fatal("watchdog") }`,
		`t.Fatalf("failed after %s", time.Since(start))`,
		`if time.Now().After(start.Add(time.Second)) { t.Fatal("condition watchdog") }`,
		`synctest.Test(t, func(t *testing.T) { if time.Since(start) > time.Second { t.Fatal("virtual clock") } })`,
	} {
		src := `package sample; import "time"; import "testing/synctest"; func TestCase(t *testing.T) { start := time.Now(); ` + expression + ` }`
		problems, err := timingProblems([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if len(problems) != 0 {
			t.Fatalf("%s: got %v", expression, problems)
		}
	}
}

func TestTimingGuardHandlesAliasedImports(t *testing.T) {
	src := `package sample; import clock "time"; func TestCase(t *testing.T) { start := clock.Now(); if clock.Since(start) > clock.Second { t.Fatal("slow") } }`
	problems, err := timingProblems([]byte(src))
	if err != nil || len(problems) != 1 {
		t.Fatalf("got %v, %v", problems, err)
	}
}

// This is deliberately narrow: short elapsed upper performance ceilings are
// unsafe on loaded runners. Lower bounds, virtual time and condition watchdogs
// have different semantics and remain review responsibilities.
func TestRepositoryHasNoShortElapsedPerformanceCeilings(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "bin", "node_modules", "dist", "superpowers":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		problems, err := timingProblems(raw)
		if err != nil {
			return err
		}
		checked++
		for _, problem := range problems {
			t.Errorf("%s:%s", path, problem)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Fatalf("checked only %d test files", checked)
	}
}
