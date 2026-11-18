//go:build !windows

package processtree

import (
	"context"
	"os/exec"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
)

func TestSharedLaunchSetsGroupBeforeExecution(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", `test "$(ps -o pgid= -p $$ | tr -d ' ')" = "$$"`)
	cmd.Env = childenv.Inherited()
	g, err := New(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if err := g.Start(cmd); err != nil {
		t.Fatal(err)
	}
	if err := g.Started(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child was not in its own group at first instruction: %v", err)
	}
}
