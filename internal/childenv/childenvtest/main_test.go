package childenvtest

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/gittest"
)

const shellProbe = "TEST_SHELL_ISOLATION_PROBE"

func TestMain(m *testing.M) {
	if os.Getenv(shellProbe) == "1" {
		os.Exit(Run(m))
	}
	gittest.Isolate()
	os.Exit(m.Run())
}

func TestSuiteIsolationDropsHostileInputsAndCleansHome(t *testing.T) {
	callerHome := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSuiteIsolationProbe$")
	// Only synthetic omo values cross this boundary, never the session's.
	cmd.Env = append(ShellEnv(os.Environ(), callerHome), shellProbe+"=1", "TEST_CALLER_HOME="+callerHome,
		"BASH_ENV=/test-owned/startup", "bAsH_env=/test-owned/mixed", "SHELLOPTS=posix", "INPUTRC=/test-owned/input",
		"OMO_AGENT_ID=fixture", "omo_socket=fixture", "OmO_FUTURE=fixture")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("suite isolation: %v\n%s", err, out)
	}
	var home string
	for line := range strings.SplitSeq(string(out), "\n") {
		if value, ok := strings.CutPrefix(line, "FIXTURE_PROBE_HOME="); ok {
			home = value
		}
	}
	if home == "" || home == callerHome {
		t.Fatal("suite did not create its own home")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("suite left its home behind: %v", err)
	}
}

func TestSuiteIsolationProbe(t *testing.T) {
	if os.Getenv(shellProbe) != "1" {
		return
	}
	for _, key := range []string{"BASH_ENV", "bAsH_env", "SHELLOPTS", "OMO_AGENT_ID", "omo_socket", "OmO_FUTURE"} {
		if _, present := os.LookupEnv(key); present {
			t.Errorf("suite inherited %s", key)
		}
	}
	if os.Getenv("INPUTRC") != os.DevNull || os.Getenv("HOME") == os.Getenv("TEST_CALLER_HOME") {
		t.Fatal("suite retained caller configuration")
	}
	fmt.Println("FIXTURE_PROBE_HOME=" + os.Getenv("HOME"))
}
