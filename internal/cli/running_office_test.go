package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

func TestRunningOfficeCallerUsesPluginOffice(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	endpoint, _, cleanup, err := transport.Endpoint(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	srv := sockd.New(endpoint, nil)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if err := os.WriteFile(filepath.Join(dir, office.LockPath), []byte(endpoint+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_AGENT_ID", "")
	t.Setenv("OMO_SOCKET", "")
	t.Setenv("OMO_OFFICE_DIR", dir)
	t.Setenv("OMO_PLUGIN_NAME", "nudge")

	gotEndpoint, gotID, err := runningOfficeCaller()
	if err != nil {
		t.Fatal(err)
	}
	if gotEndpoint != endpoint || gotID != bus.SystemSender {
		t.Fatalf("caller = (%q, %q), want (%q, %s)", gotEndpoint, gotID, endpoint, bus.SystemSender)
	}
}
