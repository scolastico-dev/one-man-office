//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func TestPluginTriggerFromRunningOfficePreservesArgumentsAndErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "omo.sock")
	srv := sockd.New(socket, nil)
	srv.Handle("plugin.trigger", func(identity string, raw json.RawMessage) (any, error) {
		var request struct {
			Name string
			Args []string
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if identity != "user" || request.Name != "report" {
			return nil, fmt.Errorf("incorrect identity or name: %s %s", identity, raw)
		}
		if len(request.Args) == 0 {
			return nil, nil
		}
		if !reflect.DeepEqual(request.Args, []string{"two words", "--flag", ""}) {
			return nil, fmt.Errorf("incorrect request: %s %s", identity, raw)
		}
		return nil, fmt.Errorf("report hook failed")
	})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })
	if err := os.WriteFile(filepath.Join(dir, office.LockPath), []byte(socket+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("OMO_SOCKET", "")
	t.Setenv("OMO_AGENT_ID", "")
	t.Setenv("OMO_OFFICE_DIR", "")
	t.Setenv("OMO_PLUGIN_NAME", "")
	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"plugin", "trigger", "report", "--", "two words", "--flag", ""})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "report hook failed") {
		t.Fatalf("trigger error = %v", err)
	}
	cmd = Root("test")
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"plugin", "trigger", "report"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "plugin report completed") {
		t.Fatalf("missing completion: %s", out.String())
	}
}
