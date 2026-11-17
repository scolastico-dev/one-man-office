package aicmd

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func contains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output %q does not contain %q", got, want)
	}
}

// audiences are every kind of agent session.
var audiences = []Audience{
	{Role: api.RoleCEO, Purpose: api.PurposeWork},
	{Role: api.RolePM, Purpose: api.PurposeWork},
	{Role: api.RoleDeveloper, Purpose: api.PurposeWork},
	{Role: api.RoleReviewer, Purpose: api.PurposeWork},
	{Role: api.RoleDeveloper, Purpose: api.PurposeExport},
}

// Every command is either an MCP tool that some agent session sees, or
// marked as the command line's only, which blocking commands and the
// user's commands are.
func TestEveryCommandIsAToolOrCommandLineOnly(t *testing.T) {
	for _, c := range Commands() {
		if c.CommandLineOnly {
			if c.Sees != nil {
				t.Errorf("%s: command line only, but has a role filter", c.Tool)
			}
			continue
		}
		if c.Sees == nil {
			t.Errorf("%s: neither a tool nor marked command line only", c.Tool)
			continue
		}
		seen := false
		for _, a := range audiences {
			seen = seen || c.Sees(a)
		}
		if !seen {
			t.Errorf("%s: no agent session sees the tool; mark it command line only", c.Tool)
		}
	}
	for _, name := range []string{"wait", "ticket_remove_workdir"} {
		if c, ok := byTool(name); !ok || !c.CommandLineOnly {
			t.Errorf("%s must be command line only", name)
		}
	}
}

func byTool(name string) (Command, bool) {
	for _, c := range Commands() {
		if c.Tool == name {
			return c, true
		}
	}
	return Command{}, false
}

var toolName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// A tool is named after its command: the path joined with _, - written _.
func TestToolNamesAreCommandPaths(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Commands() {
		want := strings.ReplaceAll(strings.Join(c.Path, "_"), "-", "_")
		if c.Tool != want || !toolName.MatchString(c.Tool) || strings.Contains(c.Tool, "__") || len("mcp__omo__"+c.Tool) > 63 {
			t.Errorf("tool %q of %v", c.Tool, c.Path)
		}
		if seen[c.Tool] {
			t.Errorf("tool %q defined twice", c.Tool)
		}
		seen[c.Tool] = true
		if c.Short == "" || c.Long == "" || c.Example == "" || c.Use == "" || !strings.HasPrefix(c.Use, c.Path[len(c.Path)-1]) {
			t.Errorf("%s: help incomplete (use %q)", c.Tool, c.Use)
		}
	}
}

// Every input is valid for the command line and for an MCP schema, and
// none takes who the caller is: the identity comes from the connection.
func TestInputsAreValid(t *testing.T) {
	for _, c := range Commands() {
		if _, err := Fields(c.Input); err != nil {
			t.Errorf("%s: %v", c.Tool, err)
		}
		s, err := jsonschema.ForType(c.Input, nil)
		if err != nil {
			t.Errorf("%s: %v", c.Tool, err)
			continue
		}
		if _, err := s.Resolve(nil); err != nil {
			t.Errorf("%s: schema: %v", c.Tool, err)
		}
		for name := range s.Properties {
			for _, bad := range []string{"session", "actor", "agent", "role", "as"} {
				if name == bad || strings.Contains(name, "session") {
					t.Errorf("%s: input field %q names the caller", c.Tool, name)
				}
			}
		}
	}
}

func TestFieldsRefusesBadInputs(t *testing.T) {
	type noTags struct {
		A string `json:"a" jsonschema:"a"`
	}
	type wordsNotLast struct {
		A string `json:"a" arg:"0..." text:"a-file" help:"x" jsonschema:"a"`
		B string `json:"b" arg:"1" jsonschema:"b"`
	}
	type gap struct {
		A string `json:"a" arg:"1" jsonschema:"a"`
	}
	type noHelp struct {
		A bool `json:"a" flag:"a" jsonschema:"a"`
	}
	type noSchema struct {
		A bool `json:"a" flag:"a" help:"a"`
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[noTags](), reflect.TypeFor[wordsNotLast](), reflect.TypeFor[gap](),
		reflect.TypeFor[noHelp](), reflect.TypeFor[noSchema]()} {
		if _, err := Fields(typ); err == nil {
			t.Errorf("%s accepted", typ)
		}
	}
}

// The coarse role filter of the tools.
func TestToolRoles(t *testing.T) {
	sees := func(tool string, a Audience) bool {
		c, ok := byTool(tool)
		if !ok {
			t.Fatalf("no tool %s", tool)
		}
		return c.Sees(a)
	}
	ceo, pm, dev, rev, exp := audiences[0], audiences[1], audiences[2], audiences[3], audiences[4]
	for _, c := range []struct {
		tool string
		a    Audience
		want bool
	}{
		{"ready", ceo, true}, {"ready", exp, true},
		{"done", ceo, false}, {"done", pm, true}, {"done", dev, true}, {"done", rev, true}, {"done", exp, true},
		{"export_text", dev, false}, {"export_text", exp, true},
		{"ticket_show", ceo, true}, {"comment", rev, true},
	} {
		if got := sees(c.tool, c.a); got != c.want {
			t.Errorf("%s for %+v: %v, want %v", c.tool, c.a, got, c.want)
		}
	}
}
