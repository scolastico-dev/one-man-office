package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelpListsOnlyUpdate(t *testing.T) {
	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "update") {
		t.Fatalf("help output missing update:\n%s", out.String())
	}
	for _, unwanted := range []string{"setup", "company", "job", "mail", "self-update"} {
		if strings.Contains(out.String(), unwanted) {
			t.Fatalf("help output contains %q:\n%s", unwanted, out.String())
		}
	}
}
