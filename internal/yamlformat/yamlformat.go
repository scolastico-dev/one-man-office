// Package yamlformat contains formatting helpers for YAML files omo edits.
package yamlformat

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"
)

// EncodePreservingBlankLines encodes node while restoring blank lines that
// separated mapping entries in the original document. yaml.v3 preserves
// comments but discards blank lines during a node round trip.
func EncodePreservingBlankLines(original []byte, node *yaml.Node, indent int) ([]byte, error) {
	var before yaml.Node
	if err := yaml.Unmarshal(original, &before); err != nil {
		return nil, err
	}
	separators := blankSeparatedPaths(original, &before)

	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(indent)
	if err := encoder.Encode(node); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	if len(separators) == 0 {
		return encoded.Bytes(), nil
	}

	var after yaml.Node
	if err := yaml.Unmarshal(encoded.Bytes(), &after); err != nil {
		return nil, err
	}
	insertBefore := map[int]int{}
	walkMappingKeys(documentRoot(&after), nil, func(path string, key *yaml.Node) {
		if count := separators[path]; count > 0 {
			insertBefore[commentStartLine(key)] = count
		}
	})
	lines := strings.Split(encoded.String(), "\n")
	out := make([]string, 0, len(lines)+len(insertBefore))
	for i, line := range lines {
		if wanted := insertBefore[i+1]; wanted > 0 && len(out) > 0 {
			existing := 0
			for j := len(out) - 1; j >= 0 && strings.TrimSpace(out[j]) == ""; j-- {
				existing++
			}
			for ; existing < wanted; existing++ {
				out = append(out, "")
			}
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n")), nil
}

func blankSeparatedPaths(raw []byte, doc *yaml.Node) map[string]int {
	lines := strings.Split(string(raw), "\n")
	paths := map[string]int{}
	walkMappingKeys(documentRoot(doc), nil, func(path string, key *yaml.Node) {
		start := commentStartLine(key)
		for line := start - 1; line >= 1 && line-1 < len(lines) && strings.TrimSpace(lines[line-1]) == ""; line-- {
			paths[path]++
		}
	})
	return paths
}

func documentRoot(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}

func commentStartLine(key *yaml.Node) int {
	line := key.Line
	if key.HeadComment != "" {
		line -= strings.Count(key.HeadComment, "\n") + 1
	}
	if line < 1 {
		return 1
	}
	return line
}

func walkMappingKeys(node *yaml.Node, parent []string, visit func(string, *yaml.Node)) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		pathParts := append(append([]string(nil), parent...), key.Value)
		path := strings.Join(pathParts, "\x00")
		visit(path, key)
		walkMappingKeys(value, pathParts, visit)
	}
}
