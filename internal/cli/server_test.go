package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/service"
)

func TestSecondStartReportsRunningServer(t *testing.T) {
	isolate(t)
	r := startRoot(t)
	out := mustExecute(t)
	if !strings.Contains(out, "omo is already running: http://omo.localhost:") {
		t.Fatalf("second start output: %q", out)
	}
	if !strings.Contains(r.out.String(), "omo: http://omo.localhost:") || !strings.Contains(r.out.String(), "also: http://127.0.0.1:") {
		t.Fatalf("first start output: %q", r.out.String())
	}
}

func TestStopStopsServer(t *testing.T) {
	isolate(t)
	r := startRoot(t)
	if out := mustExecute(t, "stop"); !strings.Contains(out, "omo stopped.") {
		t.Fatalf("stop output: %q", out)
	}
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatalf("server returned %v", err)
		}
		r.done <- nil
	case <-time.After(15 * time.Second):
		t.Fatal("server did not exit")
	}
	if _, ok, _ := service.Running(context.Background()); ok {
		t.Fatal("still running after stop")
	}
	if out := mustExecute(t, "stop"); !strings.Contains(out, "omo is not running.") {
		t.Fatalf("second stop output: %q", out)
	}
}

func TestStartAfterCrashLeftoverState(t *testing.T) {
	home := isolate(t)
	mustExecute(t, "config", "list")
	run := filepath.Join(home, "run")
	if err := os.MkdirAll(run, 0700); err != nil {
		t.Fatal(err)
	}
	stale := `{"endpoint":"127.0.0.1:1","token":"stale","url":"http://omo.localhost:1/","launch_id":"x"}`
	if err := os.WriteFile(filepath.Join(run, "runtime.json"), []byte(stale), 0600); err != nil {
		t.Fatal(err)
	}
	r := startRoot(t)
	if !strings.Contains(r.out.String(), "omo: http://omo.localhost:") {
		t.Fatalf("output: %q", r.out.String())
	}
}

func TestTokenStableAcrossRestart(t *testing.T) {
	isolate(t)
	first := startRoot(t)
	one := fragment.FindStringSubmatch(first.out.String())
	if one == nil {
		t.Fatalf("no token in %q", first.out.String())
	}
	mustExecute(t, "stop")
	<-first.done
	first.done <- nil

	ctx, cancel := context.WithCancel(context.Background())
	second := &running{out: &syncBuffer{}, done: make(chan error, 1), stop: cancel}
	cmd := Root("test")
	cmd.SetOut(second.out)
	cmd.SetErr(second.out)
	cmd.SetArgs(nil)
	go func() { second.done <- cmd.ExecuteContext(ctx) }()
	t.Cleanup(func() { second.shutdown(t) })
	deadline := time.Now().Add(20 * time.Second)
	for !fragment.MatchString(second.out.String()) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	two := fragment.FindStringSubmatch(second.out.String())
	if two == nil || two[1] != one[1] {
		t.Fatalf("token changed across restart: %v then %v", one, two)
	}
	if one[1] != tokenFile(t) {
		t.Fatal("printed token differs from the token file")
	}
}

// snapshotTree maps every path under root to its mode and content.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		out[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOldInstallationLeavesStopAndUnregisterUntouched(t *testing.T) {
	for _, args := range [][]string{{"stop"}, {"autostart", "unregister"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := isolate(t)
			run := filepath.Join(home, "run")
			if err := os.MkdirAll(run, 0700); err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string]string{
				filepath.Join(home, "config.yaml"):   "x: 1\n",
				filepath.Join(run, "runtime.json"):   "{}",
				filepath.Join(run, "autostart.json"): "{}",
			} {
				if err := os.WriteFile(name, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotTree(t, home)
			_, err := execute(context.Background(), args...)
			if err == nil || !strings.Contains(err.Error(), "omo 0.11 or older") {
				t.Fatalf("error = %v", err)
			}
			if after := snapshotTree(t, home); !reflect.DeepEqual(before, after) {
				t.Fatalf("old home modified:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func TestHelpShowsOnlyHumanCommands(t *testing.T) {
	cmd := Root("test")
	var visible []string
	for _, c := range cmd.Commands() {
		if !c.Hidden {
			visible = append(visible, c.Name())
		}
	}
	want := map[string]bool{"ai": true, "autostart": true, "config": true, "mcp": true, "stop": true, "update": true}
	for _, name := range visible {
		if !want[name] {
			t.Errorf("unexpected command %q in the command list", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("command %q missing from the command list", name)
	}
	mcpWant := []string{"install", "status", "stdio", "token", "uninstall"}
	for _, c := range cmd.Commands() {
		if c.Name() != "mcp" {
			continue
		}
		var got []string
		for _, sub := range c.Commands() {
			if !sub.Hidden {
				got = append(got, sub.Name())
				if sub.Short == "" || sub.Long == "" && sub.HasSubCommands() {
					t.Errorf("omo mcp %s has no help text", sub.Name())
				}
			}
		}
		if !slices.Equal(got, mcpWant) {
			t.Errorf("omo mcp commands %v, want %v", got, mcpWant)
		}
	}
	out, err := execute(context.Background(), "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"completion", "\n  help  "} {
		if strings.Contains(out, unwanted) {
			t.Errorf("help lists %q:\n%s", unwanted, out)
		}
	}
	if strings.Contains(out, "background-id") || strings.Contains(out, "autostart-run") {
		t.Errorf("help shows hidden internals:\n%s", out)
	}
}
