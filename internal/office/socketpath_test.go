package office

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Deep office paths use a short runtime socket and retain .omo/omo.sock as a
// discoverable link.
func TestOpenUsesShortSocketForDeepOffice(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, strings.Repeat("nested-directory-name/", 8))
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Skipf("cannot create deep path: %v", err)
	}
	yaml := `
models:
  any:
    cmd: claude
roles:
  ceo: any
  product_manager: any
  developer: any
  reviewer: any
  freelancer: any
  smokealarm: any
  firefighter: any
`
	if err := os.MkdirAll(filepath.Join(deep, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, ConfigPath), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := Open(deep, true)
	if err != nil {
		t.Fatalf("open deep office: %v", err)
	}
	defer o.Close()
	if len(o.Sup.SocketPath) > 103 {
		t.Fatalf("runtime socket remains too long: %q", o.Sup.SocketPath)
	}
	if target, err := os.Readlink(filepath.Join(deep, ".omo", "omo.sock")); err != nil || target != o.Sup.SocketPath {
		t.Fatalf("socket link target=%q err=%v, want %q", target, err, o.Sup.SocketPath)
	}
}
