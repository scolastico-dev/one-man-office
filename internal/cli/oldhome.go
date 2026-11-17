package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/oldhome"
)

// inputIsTerminal reports whether the user can answer a question on r. The
// tests replace it.
var inputIsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()))
}

// errOldInstallation ends a start that met an old installation and changed
// nothing (no terminal to ask, or the old omo still runs).
var errOldInstallation = errors.New("nothing was changed")

// meetOldInstallation runs when the omo home holds the files of omo 0.11 or
// older. It says what it found and, in a terminal, lets the user choose:
// delete it and start this omo, go back to the last terminal release, or
// quit. It returns true when the home is empty now and the start can go on.
// Without a terminal (a login autostart, a service) it changes nothing.
func meetOldInstallation(ctx context.Context, cmd *cobra.Command) (bool, error) {
	home, err := globalhome.Dir()
	if err != nil {
		return false, err
	}
	out := cmd.OutOrStdout()
	in, err := oldhome.Find(ctx, home)
	if errors.Is(err, oldhome.ErrNotOld) {
		return false, fmt.Errorf("%s is not empty and is no omo home. Empty it, or set OMO_HOME to an empty folder", home)
	}
	if err != nil {
		return false, err
	}
	describeOld(out, in)
	if !inputIsTerminal(cmd.InOrStdin()) {
		fmt.Fprintln(out, "\nomo was started without a terminal (for example at login), so it changed nothing.\nStart omo in a terminal to choose what happens to the old installation.")
		return false, errOldInstallation
	}
	input := bufio.NewReader(cmd.InOrStdin())
	fmt.Fprintf(out, `
What now?
  1  Continue: delete everything listed above (git branches stay) and start
     this omo with a fresh home.
  2  Go back: install omo %s again and leave everything as it is.
  3  Quit: change nothing.
`, oldhome.LastRelease)
	switch ask(input, out, "Choose 1, 2 or 3 [3]: ") {
	case "1":
		return continueOverOld(ctx, out, input, in)
	case "2":
		return false, goBackToOld(ctx, out, in)
	default:
		fmt.Fprintln(out, "Nothing was changed.")
		return false, nil
	}
}

func describeOld(out io.Writer, in oldhome.Installation) {
	fmt.Fprintf(out, `omo found the files of omo 0.11 or older in %s.

This omo is a different program: one board in the browser for all your
projects, instead of an office per folder in a terminal. It cannot use
anything the old omo kept: offices, jobs, mail, model profiles, prompts,
plugins and settings are not carried over. Your repositories and their
branches are not touched.

What the old omo left:
  home folder      %s (%s)
`, in.Home, in.Home, strings.Join(in.Entries, ", "))
	for _, o := range in.Offices {
		fmt.Fprintf(out, "  office           %s (its .omo folder, %s)\n", o.Dir, plural(len(o.Worktrees), "worktree", "worktrees"))
		for _, w := range o.Worktrees {
			mark := ""
			switch {
			case w.Unknown:
				mark = "   CANNOT BE CHECKED: treated as uncommitted changes"
			case w.Dirty:
				mark = "   UNCOMMITTED CHANGES"
			}
			fmt.Fprintf(out, "    worktree       %s%s\n", w.Path, mark)
		}
	}
	if in.Autostart != "" {
		fmt.Fprintf(out, "  login autostart  %s\n", in.Autostart)
	}
	for _, dir := range in.Gone {
		fmt.Fprintf(out, "  (not found)      %s: trusted once, its .omo folder is gone\n", dir)
	}
	fmt.Fprintln(out, "Offices you never trusted are not in the old list: their .omo folders stay as they are.")
	if running := in.Running(); len(running) > 0 {
		fmt.Fprintf(out, "\nThe old omo is running: %s.\n", strings.Join(running, "; "))
	}
}

func continueOverOld(ctx context.Context, out io.Writer, input *bufio.Reader, in oldhome.Installation) (bool, error) {
	if running := in.Running(); len(running) > 0 {
		fmt.Fprintf(out, "Stop the old omo first (%s): quit its terminal or dashboard. Then start omo again. Nothing was changed.\n", strings.Join(running, "; "))
		return false, errOldInstallation
	}
	if dirty := in.Dirty(); len(dirty) > 0 {
		fmt.Fprintf(out, "%s marked above %s work nobody committed; it is deleted with the rest.\n", plural(len(dirty), "worktree", "worktrees"), map[bool]string{true: "holds", false: "hold"}[len(dirty) == 1])
		if ask(input, out, "Type delete to delete everything listed above: ") != "delete" {
			fmt.Fprintln(out, "Nothing was changed.")
			return false, nil
		}
	} else if a := strings.ToLower(ask(input, out, "Delete everything listed above? [y/N] ")); a != "y" && a != "yes" {
		fmt.Fprintln(out, "Nothing was changed.")
		return false, nil
	}
	if err := in.Remove(ctx, func(line string) { fmt.Fprintln(out, "  "+line) }); err != nil {
		return false, err
	}
	fmt.Fprintln(out, "The old installation is removed. Starting omo.")
	return true, nil
}

func goBackToOld(ctx context.Context, out io.Writer, in oldhome.Installation) error {
	release, err := releaseByTag(ctx, oldhome.LastRelease)
	if err != nil {
		return fmt.Errorf("find omo %s: %w; nothing was changed", oldhome.LastRelease, err)
	}
	target, err := currentExecutable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	if err := installRelease(ctx, release, target); err != nil {
		return fmt.Errorf("install omo %s: %w; nothing was changed", oldhome.LastRelease, err)
	}
	fmt.Fprintf(out, "Installed omo %s at %s.\n", release.Tag, target)
	for _, o := range in.Offices {
		if o.Config == "" {
			continue
		}
		if err := oldhome.DisableUpdateCheck(o.Config); err != nil {
			fmt.Fprintf(out, "  could not switch off the update check in %s: %v\n", o.Config, err)
			continue
		}
		fmt.Fprintf(out, "  %s: the update check at start is off (startup.check_self_update: false)\n", o.Config)
	}
	fmt.Fprintf(out, `Start omo as before. The offices above no longer offer a newer omo at their
start; other offices still do: answer no there. When you want this version,
run omo self-update.
`)
	return nil
}

// ask prints prompt and returns the answer without spaces; "" at the end
// of the input.
func ask(input *bufio.Reader, out io.Writer, prompt string) string {
	fmt.Fprint(out, prompt)
	line, err := input.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
	}
	return strings.TrimSpace(line)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Explain adds what to do to an error of the command line itself (an
// unknown command or flag) when the omo home is one of omo 0.11 or older:
// such a call most likely comes from the old omo's habits (its agents, its
// login autostart entry, a script) or from the old omo restarting itself
// after it installed this version.
func Explain(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "unknown command") && !strings.HasPrefix(msg, "unknown flag") && !strings.HasPrefix(msg, "unknown shorthand flag") {
		return err
	}
	home, herr := globalhome.Dir()
	if herr != nil || !oldhome.IsOld(home) {
		return err
	}
	return fmt.Errorf("%w\nThis omo replaced omo 0.11 or older, whose commands and flags it does not have.\nStart omo without arguments in a terminal to choose what happens to the old installation in %s", err, home)
}
