//go:build !windows

package childenvtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Controls use only owned inputs: never run an inherited startup file, even
// when proving that a shell consumes the configuration the fixture removes.
func TestShellEnvPreventsHostileShellConfiguration(t *testing.T) {
	home := t.TempDir()
	startup := filepath.Join(home, "startup")
	if err := os.WriteFile(startup, []byte("exit 93\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte("exit 93\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(home, "inputrc")
	if err := os.WriteFile(input, []byte("set editing-mode vi\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, shell        string
		args               []string
		extra, wantControl []string
		clean              string
	}{
		{"BashStartup", "bash", []string{"--norc", "-c", "printf fixture-ready"}, []string{"BASH_ENV=" + startup}, nil, "fixture-ready"},
		{"POSIXStartup", "bash", []string{"--norc", "--posix", "-ic", "printf fixture-ready"}, []string{"ENV=" + startup}, nil, "fixture-ready"},
		{"ZshStartup", "zsh", []string{"-c", "printf fixture-ready"}, []string{"ZDOTDIR=" + home}, nil, "fixture-ready"},
		{"BashOptions", "bash", []string{"--norc", "-c", "if shopt -qo posix; then exit 93; fi; printf fixture-ready"}, []string{"SHELLOPTS=posix"}, nil, "fixture-ready"},
		{"BashGlobOptions", "bash", []string{"--norc", "-c", "set -- missing-fixture-glob-*; if [ \"$#\" = 0 ]; then exit 93; fi; printf fixture-ready"}, []string{"BASHOPTS=nullglob"}, nil, "fixture-ready"},
		{"BashFunction", "bash", []string{"--norc", "-c", "if type probe >/dev/null 2>&1; then probe; fi; printf fixture-ready"}, []string{"BASH_FUNC_probe%%=() { exit 93; }"}, nil, "fixture-ready"},
		{"ReadlineInput", "bash", []string{"--norc", "-ic", "bind -v"}, []string{"INPUTRC=" + input}, []string{"set editing-mode vi"}, "set editing-mode emacs"},
		{"ZshEditor", "zsh", []string{"-fic", "bindkey -lL main"}, []string{"EDITOR=vi", "VISUAL=vi"}, []string{"viins main"}, "emacs main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shell, err := exec.LookPath(tc.shell)
			if err != nil {
				t.Skip(tc.shell + " is not installed")
			}
			// The control HOME is empty; ZDOTDIR points to a separate owned home.
			base := append(ShellEnv(nil, t.TempDir()), "PATH="+os.Getenv("PATH"))
			base = append(base, tc.extra...)
			if tc.name == "BashGlobOptions" {
				// BASHOPTS appeared in Bash 4.1; macOS still ships Bash 3.2.
				// Probe without the injected variable so an ordinary imported
				// environment value cannot look like native shell support.
				probe := exec.Command(shell, "--norc", "-c", `test "${BASHOPTS+x}" = x`)
				probe.Env = ShellEnv(base, t.TempDir())
				if err := probe.Run(); err != nil {
					if status, ok := err.(*exec.ExitError); ok && status.ExitCode() == 1 {
						t.Skip("Bash lacks BASHOPTS; portable ShellEnv boundary still checks removal")
					}
					t.Fatal(err)
				}
			}
			run := func(env []string) (string, error) {
				cmd := exec.Command(shell, tc.args...)
				cmd.Env = env
				cmd.Dir = t.TempDir()
				out, err := cmd.Output()
				return string(out), err
			}
			out, err := run(base)
			if tc.wantControl == nil {
				if status, ok := err.(*exec.ExitError); !ok || status.ExitCode() != 93 {
					t.Fatalf("owned control did not exit93: %v, %q", err, out)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range tc.wantControl {
					if !strings.Contains(out, want) {
						t.Fatalf("owned control lacks %q: %q", want, out)
					}
				}
			}
			out, err = run(ShellEnv(base, t.TempDir()))
			if err != nil || !strings.Contains(out, tc.clean) {
				t.Fatalf("isolated shell: %v, %q; want %q", err, out, tc.clean)
			}
		})
	}
}
