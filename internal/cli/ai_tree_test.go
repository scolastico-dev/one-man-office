package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
)

// TestCobraTreeMatchesCommands keeps `omo ai` and internal/aicmd one: every
// command of `omo ai` is a definition and every definition a command, every
// group a defined group, the help is the definition's, and every flag and
// argument is a field of the command's input.
func TestCobraTreeMatchesCommands(t *testing.T) {
	ai, _, err := Root("test").Find([]string{"ai"})
	if err != nil {
		t.Fatal(err)
	}
	defs := map[string]aicmd.Command{}
	for _, c := range aicmd.Commands() {
		defs[strings.Join(c.Path, " ")] = c
	}
	groups := map[string]aicmd.Group{}
	for _, g := range aicmd.Groups() {
		groups[strings.Join(g.Path, " ")] = g
	}
	seenDefs, seenGroups := map[string]bool{}, map[string]bool{}
	var walk func(cmd *cobra.Command, path []string)
	walk = func(cmd *cobra.Command, path []string) {
		key := strings.Join(path, " ")
		if cmd.HasSubCommands() {
			g, ok := groups[key]
			if !ok {
				t.Errorf("omo ai %s: a group without a definition", key)
			} else if cmd.Use != g.Use || cmd.Short != g.Short || cmd.Long != g.Long || cmd.Example != g.Example {
				t.Errorf("omo ai %s: the help is not the group's definition", key)
			}
			seenGroups[key] = true
			for _, sub := range cmd.Commands() {
				if sub.Name() == "help" {
					continue
				}
				walk(sub, append(slices.Clone(path), sub.Name()))
			}
			return
		}
		def, ok := defs[key]
		if !ok {
			t.Errorf("omo ai %s has no definition in internal/aicmd", key)
			return
		}
		seenDefs[key] = true
		if cmd.Use != def.Use || cmd.Short != def.Short || cmd.Long != def.Long || cmd.Example != def.Example {
			t.Errorf("omo ai %s: the help is not the definition's", key)
		}
		fields, err := aicmd.Fields(def.Input)
		if err != nil {
			t.Errorf("omo ai %s: %v", key, err)
			return
		}
		flags := map[string]aicmd.Field{}
		for _, f := range fields {
			if f.Flag != "" {
				flags[f.Flag] = f
			}
		}
		cmd.LocalFlags().VisitAll(func(fl *pflag.Flag) {
			if fl.Name == "help" {
				return
			}
			f, ok := flags[fl.Name]
			if !ok {
				t.Errorf("omo ai %s --%s is no field of the input", key, fl.Name)
				return
			}
			if fl.Usage != f.Help || (f.Default != "" && fl.DefValue != f.Default) {
				t.Errorf("omo ai %s --%s: usage %q, default %q are not the definition's", key, fl.Name, fl.Usage, fl.DefValue)
			}
			delete(flags, fl.Name)
		})
		for name := range flags {
			t.Errorf("omo ai %s: field flag --%s is not a flag of the command", key, name)
		}
	}
	walk(ai, nil)
	for key := range defs {
		if !seenDefs[key] {
			t.Errorf("definition %q is no command of omo ai", key)
		}
	}
	for key := range groups {
		if !seenGroups[key] {
			t.Errorf("group %q is no group of omo ai", key)
		}
	}
	if len(seenDefs) < 20 {
		t.Fatalf("only %d commands found", len(seenDefs))
	}
}
