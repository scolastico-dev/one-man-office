package secreturl

import (
	"slices"
	"strings"
	"testing"
)

func TestDisplayMasksPasswordAndQueryValues(t *testing.T) {
	for raw, want := range map[string]string{
		"https://host/mcp":                    "https://host/mcp",
		"https://host/mcp?api_key=s3cr3t&v=2": "https://host/mcp?api_key=***&v=***",
		"https://user:pa%20ss@host/mcp":       "https://user:***@host/mcp",
		"https://host/mcp?token=a%2Bb%3D":     "https://host/mcp?token=***",
		"http://[::1]:8080/sse?k=1":           "http://[::1]:8080/sse?k=***",
		"https://host/mcp?flag":               "https://host/mcp?flag=***",
		"://not a url":                        Mask,
	} {
		if got := Display(raw); got != want {
			t.Errorf("Display(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestValuesAndForms(t *testing.T) {
	raw := "https://user:pa%20ss@host/mcp?key=a%2Bb%3Dc&other=plain"
	values := Values(raw)
	for _, want := range []string{"pa ss", "pa%20ss", "a%2Bb%3Dc", "a+b=c", "plain"} {
		if !slices.Contains(values, want) {
			t.Errorf("Values misses %q: %v", want, values)
		}
	}
	forms := Forms(raw)
	if !slices.Contains(forms, raw) || !slices.ContainsFunc(forms, func(f string) bool { return strings.Contains(f, "user:xxxxx@") }) {
		t.Errorf("forms %v", forms)
	}
	if Forms("https://host/mcp") != nil || HasSecrets("https://host/mcp") {
		t.Error("an address without secrets has forms")
	}
}
