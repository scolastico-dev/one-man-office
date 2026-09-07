package yamlformat

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEncodePreservingBlankLinesKeepsNestedAndCommentedBoundaries(t *testing.T) {
	original := []byte("first:\n  one: 1\n\n\n  # second block\n  two: 2\n\n# top block\nsecond:\n  three: 3\n")
	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil {
		t.Fatal(err)
	}
	root := doc.Content[0]
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "added"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"},
	)
	written, err := EncodePreservingBlankLines(original, &doc, 2)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	if !strings.Contains(text, "  one: 1\n\n\n  # second block\n  two: 2") {
		t.Fatalf("nested boundary was not preserved:\n%s", text)
	}
	if !strings.Contains(text, "  two: 2\n\n# top block\nsecond:") {
		t.Fatalf("commented top-level boundary was not preserved:\n%s", text)
	}
	if !strings.Contains(text, "added: value") {
		t.Fatalf("modified node was not encoded:\n%s", text)
	}
}
