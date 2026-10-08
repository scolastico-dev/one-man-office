//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func TestSmokeReadyCLIEndsWithReminderAfterWhitespace(t *testing.T) {
	readyPrompt, err := prompts.Render(t.TempDir(), "smokealarm", prompts.Data{Name: "smoke-test", Role: "smokealarm", Goal: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "omo.sock")
	srv := sockd.New(socket, nil)
	srv.Handle("ready", func(_ string, _ json.RawMessage) (any, error) {
		return proto.ReadyResponse{Prompt: readyPrompt}, nil
	})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })
	t.Setenv("OMO_SOCKET", socket)
	t.Setenv("OMO_AGENT_ID", "smoke-test")
	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"ready"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	const lastInstruction = `Then end immediately with: omo done "round complete: <0|1> incidents"`
	if !strings.HasSuffix(strings.TrimRight(out.String(), " \t\r\n"), lastInstruction) {
		t.Fatalf("CLI ready output does not end with smoke reminder: %q", out.String()[max(0, out.Len()-180):])
	}
}
