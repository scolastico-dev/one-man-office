package childenvtest

import (
	"os"
	"os/exec"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/processtree"
)

// GoBuild compiles a fixture without session identity, external C tools or
// Git VCS hooks, preserving the compiler's existing cache inputs.
func GoBuild(t testing.TB, dir, output string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", output, ".")
	cmd.Dir = dir
	cmd.Env = append(WithoutOmo(os.Environ()), "CGO_ENABLED=0")
	if out, err := processtree.CombinedOutput(cmd); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
}
